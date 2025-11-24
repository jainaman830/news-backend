package models

import "time"

type Article struct {
	ID             string    `bson:"id" json:"id"`
	Title          string    `bson:"title" json:"title"`
	Description    string    `bson:"description" json:"description"`
	URL            string    `bson:"url" json:"url"`
	PublicationRaw string    `bson:"publication_date" json:"publication_date"`
	Publication    time.Time `bson:"-" json:"-"`
	SourceName     string    `bson:"source_name" json:"source_name"`
	Category       []string  `bson:"category" json:"category"`
	RelevanceScore float64   `bson:"relevance_score" json:"relevance_score"`
	Latitude       float64   `bson:"latitude" json:"latitude"`
	Longitude      float64   `bson:"longitude" json:"longitude"`
}
