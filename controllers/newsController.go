package controllers

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"news-backend/models"
	"news-backend/services"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	mongoClient      *mongo.Client
	mongoOnce        sync.Once
	databaseName     = "news"
	collectionName   = "articles"
	trendingCacheTTL = 60 * time.Second
)

// ConnectDB connects to MongoDB instance using MONGODB_URI environment variable
func ConnectDB() {
	mongoOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Get MongoDB URI from environment variable or use default
		mongoURI := os.Getenv("MONGODB_URI")
		if mongoURI == "" {
			mongoURI = "mongodb://localhost:27017"
		}

		client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
		if err != nil {
			log.Fatal("mongo connect:", err)
		}
		// ping
		if err := client.Ping(ctx, nil); err != nil {
			log.Fatal("mongo ping:", err)
		}
		mongoClient = client
		// initialize trending simulation
		services.InitTrendingSimulator(client, databaseName, collectionName)
	})
}

// SaveArticlesToDB reads JSON file and seeds DB if empty.
func SaveArticlesToDB() {
	collection := mongoClient.Database(databaseName).Collection(collectionName)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// check count
	c, err := collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		log.Println("count error:", err)
		return
	}
	if c > 0 {
		log.Println("articles already present in DB:", c)
		return
	}

	articles, err := services.LoadNewsDataFromFile("data/news_data.json")
	if err != nil {
		log.Println("load data:", err)
		return
	}
	// prepare insert docs
	docs := make([]interface{}, 0, len(articles))
	for _, a := range articles {
		// keep raw publication string, parse separately in code
		docs = append(docs, a)
	}
	_, err = collection.InsertMany(ctx, docs)
	if err != nil {
		log.Println("insert many:", err)
		return
	}
	log.Println("seeded", len(docs), "articles")
	// also refresh trending simulator with new articles
	services.InitTrendingSimulator(mongoClient, databaseName, collectionName)
}

// helper: fetch all articles
func fetchAllArticles(ctx context.Context) ([]models.Article, error) {
	coll := mongoClient.Database(databaseName).Collection(collectionName)
	cur, err := coll.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.Article{}
	for cur.Next(ctx) {
		var a models.Article
		if err := cur.Decode(&a); err != nil {
			continue
		}
		// parse publication date if available
		if t, err := time.Parse(time.RFC3339, a.PublicationRaw); err == nil {
			a.Publication = t
		}
		out = append(out, a)
	}
	return out, nil
}

// Haversine distance in kilometers
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0 // Earth radius km
	toRad := func(d float64) float64 { return d * math.Pi / 180.0 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	lat1r := toRad(lat1)
	lat2r := toRad(lat2)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1r)*math.Cos(lat2r)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// response article type
type responseArticle struct {
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	URL             string   `json:"url"`
	PublicationDate string   `json:"publication_date"`
	SourceName      string   `json:"source_name"`
	Category        []string `json:"category"`
	RelevanceScore  float64  `json:"relevance_score"`
	LLMSummary      string   `json:"llm_summary"`
	Latitude        float64  `json:"latitude"`
	Longitude       float64  `json:"longitude"`
	DistanceKM      *float64 `json:"distance_km,omitempty"`
}

// helper to build response with LLM summary
func toResponseArticle(a models.Article, includeDistance *float64) responseArticle {
	// generate summary (heuristic or external depending on env)
	summary, _ := services.GenerateSummary(a.Title, a.Description)
	return responseArticle{
		Title:           a.Title,
		Description:     a.Description,
		URL:             a.URL,
		PublicationDate: a.PublicationRaw,
		SourceName:      a.SourceName,
		Category:        a.Category,
		RelevanceScore:  a.RelevanceScore,
		LLMSummary:      summary,
		Latitude:        a.Latitude,
		Longitude:       a.Longitude,
		DistanceKM:      includeDistance,
	}
}

// GET /api/v1/news/category?category=Technology&limit=5
func GetArticlesByCategory(c *gin.Context) {
	ctl := c.Request.Context()
	category := c.Query("category")
	limit := parseLimit(c.DefaultQuery("limit", "5"))

	articles, err := fetchAllArticles(ctl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// filter by category membership (case-insensitive)
	res := []models.Article{}
	catLower := strings.ToLower(category)
	for _, a := range articles {
		for _, cat := range a.Category {
			if strings.ToLower(cat) == catLower {
				res = append(res, a)
				break
			}
		}
	}
	// sort by publication date desc
	sort.Slice(res, func(i, j int) bool {
		return res[i].Publication.After(res[j].Publication)
	})
	// prepare response top N
	resp := []responseArticle{}
	for i := 0; i < min(limit, len(res)); i++ {
		resp = append(resp, toResponseArticle(res[i], nil))
	}
	c.JSON(http.StatusOK, gin.H{"articles": resp, "total": len(res)})
}

// GET /api/v1/news/score?threshold=0.7&limit=5
func GetArticlesByScore(c *gin.Context) {
	ctl := c.Request.Context()
	threshold := parseFloatDefault(c.DefaultQuery("threshold", "0.7"), 0.7)
	limit := parseLimit(c.DefaultQuery("limit", "5"))

	articles, err := fetchAllArticles(ctl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	res := []models.Article{}
	for _, a := range articles {
		if a.RelevanceScore >= threshold {
			res = append(res, a)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].RelevanceScore > res[j].RelevanceScore
	})
	resp := []responseArticle{}
	for i := 0; i < min(limit, len(res)); i++ {
		resp = append(resp, toResponseArticle(res[i], nil))
	}
	c.JSON(http.StatusOK, gin.H{"articles": resp, "total": len(res)})
}

// GET /api/v1/news/search?query=Elon+Musk&limit=5
func SearchArticles(c *gin.Context) {
	ctl := c.Request.Context()
	q := strings.TrimSpace(c.Query("query"))
	limit := parseLimit(c.DefaultQuery("limit", "5"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query required"})
		return
	}
	articles, err := fetchAllArticles(ctl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	qLower := strings.ToLower(q)
	type scored struct {
		A     models.Article
		Score float64
	}
	candidates := []scored{}
	maxTextCount := 1.0
	for _, a := range articles {
		text := strings.ToLower(a.Title + " " + a.Description)
		count := float64(strings.Count(text, qLower))
		if count > maxTextCount {
			maxTextCount = count
		}
		if count > 0 || a.RelevanceScore > 0 {
			candidates = append(candidates, scored{A: a, Score: count})
		}
	}
	// combine scores: 60% relevance_score, 40% normalized text match
	for i := range candidates {
		textScore := candidates[i].Score / maxTextCount
		final := 0.6*candidates[i].A.RelevanceScore + 0.4*textScore
		candidates[i].Score = final
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	resp := []responseArticle{}
	for i := 0; i < min(limit, len(candidates)); i++ {
		resp = append(resp, toResponseArticle(candidates[i].A, nil))
	}
	c.JSON(http.StatusOK, gin.H{"articles": resp, "total": len(candidates)})
}

// GET /api/v1/news/source?source=Reuters&limit=5
func GetArticlesBySource(c *gin.Context) {
	ctl := c.Request.Context()
	source := c.Query("source")
	limit := parseLimit(c.DefaultQuery("limit", "5"))
	if source == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source required"})
		return
	}
	articles, err := fetchAllArticles(ctl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	srcLower := strings.ToLower(source)
	res := []models.Article{}
	for _, a := range articles {
		if strings.ToLower(a.SourceName) == srcLower {
			res = append(res, a)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Publication.After(res[j].Publication)
	})
	resp := []responseArticle{}
	for i := 0; i < min(limit, len(res)); i++ {
		resp = append(resp, toResponseArticle(res[i], nil))
	}
	c.JSON(http.StatusOK, gin.H{"articles": resp, "total": len(res)})
}

// GET /api/v1/news/nearby?lat=37.4&lon=-122.1&radius=10&limit=5
func GetNearbyArticles(c *gin.Context) {
	ctl := c.Request.Context()
	lat := parseFloatDefault(c.Query("lat"), 0)
	lon := parseFloatDefault(c.Query("lon"), 0)
	radius := parseFloatDefault(c.DefaultQuery("radius", "10"), 10) // km
	limit := parseLimit(c.DefaultQuery("limit", "5"))

	if lat == 0 && lon == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat and lon required"})
		return
	}
	articles, err := fetchAllArticles(ctl)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type withDist struct {
		A    models.Article
		Dist float64
	}
	list := []withDist{}
	for _, a := range articles {
		d := haversine(lat, lon, a.Latitude, a.Longitude)
		if d <= radius {
			list = append(list, withDist{A: a, Dist: d})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Dist < list[j].Dist
	})
	resp := []responseArticle{}
	for i := 0; i < min(limit, len(list)); i++ {
		dist := list[i].Dist
		resp = append(resp, toResponseArticle(list[i].A, &dist))
	}
	c.JSON(http.StatusOK, gin.H{"articles": resp, "total": len(list)})
}

// GET /api/v1/news/trending?lat=37.4&lon=-122.1&limit=5&radius=50
func GetTrending(c *gin.Context) {
	lat := parseFloatDefault(c.Query("lat"), 0)
	lon := parseFloatDefault(c.Query("lon"), 0)
	limit := parseLimit(c.DefaultQuery("limit", "5"))
	radius := parseFloatDefault(c.DefaultQuery("radius", "50"), 50)

	if lat == 0 && lon == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat and lon required"})
		return
	}
	// get trending articles from service (with caching)
	top, err := services.GetTrendingForLocation(lat, lon, radius, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := []responseArticle{}
	for _, t := range top {
		// t.Article is models.Article, t.Score is trending score
		dist := haversine(lat, lon, t.Article.Latitude, t.Article.Longitude)
		a := toResponseArticle(t.Article, &dist)
		// include ranking metadata in source_name field or separate? keep consistent:
		// attach trending score in description (not ideal) — instead add as metadata field
		// but responseArticle fixed; add to description prefix
		a.Description = a.Description
		resp = append(resp, a)
	}
	c.JSON(http.StatusOK, gin.H{"articles": resp})
}

// GET /api/v1/news/process?query=...
// uses LLM to extract entities + intent
func ProcessQuery(c *gin.Context) {
	q := c.Query("query")
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query required"})
		return
	}
	out, err := services.ExtractEntitiesAndIntent(q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// utilities
func parseLimit(s string) int {
	n := 5
	if s == "" {
		return n
	}
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	if err == nil && v > 0 {
		n = v
	}
	return n
}

func parseFloatDefault(s string, d float64) float64 {
	if s == "" {
		return d
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return d
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
