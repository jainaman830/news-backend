package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"sort"
	"sync"
	"time"

	"news-backend/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Event struct {
	ArticleID string    `bson:"article_id"`
	Type      string    `bson:"event_type"` // view, click, share
	Lat       float64   `bson:"lat"`
	Lon       float64   `bson:"lon"`
	Ts        time.Time `bson:"ts"`
}

type trendingItem struct {
	Article models.Article
	Score   float64
}

var (
	eventsMu sync.RWMutex
	events   []Event

	cacheMu sync.RWMutex
	cache   = map[string]cachedTrending{}
)

type cachedTrending struct {
	At     time.Time
	Result []trendingItem
}

// InitTrendingSimulator loads articles and simulates events around them.
func InitTrendingSimulator(client *mongo.Client, dbName, collName string) {
	// generate initial simulated event stream stored in memory
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		coll := client.Database(dbName).Collection(collName)
		cur, err := coll.Find(ctx, bson.M{})
		if err != nil {
			return
		}
		defer cur.Close(ctx)
		articles := []models.Article{}
		for cur.Next(ctx) {
			var a models.Article
			if err := cur.Decode(&a); err == nil {
				articles = append(articles, a)
			}
		}
		simulateEvents(articles)
	}()
}

// simulateEvents creates a list of events distributed among articles with timestamps over last 48h
func simulateEvents(articles []models.Article) {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	events = []Event{}
	if len(articles) == 0 {
		return
	}
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	now := time.Now()
	numEvents := 1000 + rnd.Intn(2000)
	types := []string{"view", "click", "share"}
	for i := 0; i < numEvents; i++ {
		a := articles[rnd.Intn(len(articles))]
		// jitter location slightly around article
		lat := a.Latitude + (rnd.Float64()-0.5)*0.05
		lon := a.Longitude + (rnd.Float64()-0.5)*0.05
		// timestamp within last 48 hours, bias to recent
		age := time.Duration(rnd.Intn(48*60)) * time.Minute
		ts := now.Add(-age)
		events = append(events, Event{
			ArticleID: a.ID,
			Type:      types[rnd.Intn(len(types))],
			Lat:       lat,
			Lon:       lon,
			Ts:        ts,
		})
	}
}

// GetTrendingForLocation computes trending articles near lat/lon within radius (km) and returns top limit results.
// Uses a small cache keyed by rounded lat/lon+radius.
func GetTrendingForLocation(lat, lon, radius float64, limit int) ([]trendingItem, error) {
	key := cacheKey(lat, lon, radius)
	// quick cached hit
	cacheMu.RLock()
	if e, ok := cache[key]; ok {
		if time.Since(e.At) < 60*time.Second {
			cacheMu.RUnlock()
			// return top limit
			if len(e.Result) > limit {
				return e.Result[:limit], nil
			}
			return e.Result, nil
		}
	}
	cacheMu.RUnlock()

	// compute trending
	eventsMu.RLock()
	evs := make([]Event, len(events))
	copy(evs, events)
	eventsMu.RUnlock()

	// aggregate per article with recency decay and geo relevance
	now := time.Now()
	scoreMap := map[string]float64{}
	// weights for event types
	weights := map[string]float64{"view": 1.0, "click": 2.0, "share": 3.0}

	for _, e := range evs {
		// filter events by radius from provided lat/lon (geographical relevance)
		d := haversine(lat, lon, e.Lat, e.Lon) // km
		if d > radius {
			continue
		}
		ageHours := now.Sub(e.Ts).Hours()
		// recency decay: half-life 12 hours -> weight multiply exp(-ln2 * age / 12)
		decay := math.Exp(-math.Ln2 * ageHours / 12.0)
		w := weights[e.Type]
		scoreMap[e.ArticleID] += w * decay * (1.0 / (1.0 + d)) // nearer events matter more
	}

	// fetch article details for scored articles
	// we need Mongo client? load via LoadArticlesByIDs
	ids := make([]string, 0, len(scoreMap))
	for id := range scoreMap {
		ids = append(ids, id)
	}
	articles := []models.Article{}
	if len(ids) > 0 {
		articles = loadArticlesByIDs(ids)
	}

	// build items
	items := []trendingItem{}
	for _, a := range articles {
		items = append(items, trendingItem{
			Article: a,
			Score:   scoreMap[a.ID],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})

	// cache result (keep full list)
	cacheMu.Lock()
	cache[key] = cachedTrending{At: time.Now(), Result: items}
	cacheMu.Unlock()

	if len(items) > limit {
		return items[:limit], nil
	}
	return items, nil
}

func cacheKey(lat, lon, radius float64) string {
	// coarse grid: round to 2 decimals (~1km), include radius
	rlat := math.Round(lat*100) / 100.0
	rlon := math.Round(lon*100) / 100.0
	rr := math.Round(radius*10) / 10.0
	return fmt.Sprintf("%.2f:%.2f:%.1f", rlat, rlon, rr)
}

// loadArticlesByIDs reads DB and returns Articles for given ids
func loadArticlesByIDs(ids []string) []models.Article {
	// naive DB read: use global mongo client via services.GetMongoClient if present
	// We call LoadNewsDataFromFile as fallback if DB not accessible
	articles, err := LoadAllArticlesFromDB()
	if err != nil {
		// fallback
		all, _ := LoadNewsDataFromFile("data/news_data.json")
		idset := map[string]bool{}
		for _, id := range ids {
			idset[id] = true
		}
		res := []models.Article{}
		for _, a := range all {
			if idset[a.ID] {
				res = append(res, a)
			}
		}
		return res
	}
	idset := map[string]bool{}
	for _, id := range ids {
		idset[id] = true
	}
	out := []models.Article{}
	for _, a := range articles {
		if idset[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// helper to load all articles from DB using mongo client from controllers package
func LoadAllArticlesFromDB() ([]models.Article, error) {
	// Get MongoDB URI from environment variable or use default
	mongoURI := os.Getenv("MONGODB_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer client.Disconnect(ctx)
	coll := client.Database("news").Collection("articles")
	cur, err := coll.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.Article{}
	for cur.Next(ctx) {
		var a models.Article
		if err := cur.Decode(&a); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// reuse haversine from controllers
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180.0 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	lat1r := toRad(lat1)
	lat2r := toRad(lat2)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1r)*math.Cos(lat2r)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// LoadNewsDataFromFile reads data/news_data.json into []models.Article
func LoadNewsDataFromFile(path string) ([]models.Article, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}
	var arr []models.Article
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}
