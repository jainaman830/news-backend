package services

import (
	"fmt"
	"log"
	"strings"
)

type QueryAnalysis struct {
	Entities   []string `json:"entities"`
	Categories []string `json:"categories,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Intent     string   `json:"intent"`
	Location   *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location,omitempty"`
}

// ExtractEntitiesAndIntent processes a natural language query and extracts relevant information
func ExtractEntitiesAndIntent(query string) (QueryAnalysis, error) {
	// In a real implementation, this would call an actual LLM API
	// For now, we'll implement a simple rule-based approach
	// that can be replaced with an actual LLM call later

	// Convert query to lowercase for case-insensitive matching
	q := strings.ToLower(query)
	result := QueryAnalysis{
		Entities:   []string{},
		Categories: []string{},
		Sources:    []string{},
		Intent:     "search", // default intent
	}

	// Check for location-related queries
	if strings.Contains(q, "near me") || strings.Contains(q, "nearby") ||
		strings.Contains(q, "close to") || strings.Contains(q, "around") {
		result.Intent = "nearby"
	}

	// Check for category-related queries
	categories := []string{"technology", "business", "sports", "entertainment", "health", "science", "politics"}
	for _, cat := range categories {
		if strings.Contains(q, cat) {
			result.Categories = append(result.Categories, cat)
			result.Intent = "category"
		}
	}

	// Check for source-related queries
	sources := []string{"new york times", "reuters", "bbc", "cnn", "the guardian"}
	for _, src := range sources {
		if strings.Contains(q, src) {
			result.Sources = append(result.Sources, src)
			result.Intent = "source"
		}
	}

	// Enhanced entity extraction with known entities and better handling of proper nouns
	knownEntities := map[string]bool{
		"india": true, "russia": true, "usa": true, "china": true, "uk": true,
		"technology": true, "business": true, "sports": true, "politics": true,
		"modi": true, "putin": true, "biden": true, "election": true,
	}

	// First check for known entities
	lowerQuery := strings.ToLower(q)
	for entity := range knownEntities {
		if strings.Contains(lowerQuery, entity) {
			// Only add if not already in entities
			found := false
			for _, e := range result.Entities {
				if strings.EqualFold(e, entity) {
					found = true
					break
				}
			}
			if !found {
				result.Entities = append(result.Entities, strings.Title(entity))
			}
		}
	}

	// Then look for proper nouns (words that are capitalized in the original query)
	words := strings.Fields(q)
	for i, word := range words {
		// Skip common words and short words
		if len(word) < 3 || isCommonWord(strings.ToLower(word)) {
			continue
		}

		// Check if word is capitalized (potential proper noun)
		if len(word) > 0 && word[0] >= 'A' && word[0] <= 'Z' {
			// Skip if it's the first word (might be start of sentence)
			if i == 0 {
				continue
			}
			// Clean the word (remove punctuation)
			cleanWord := strings.Trim(word, ",.!?;:")
			if len(cleanWord) >= 3 && !isCommonWord(strings.ToLower(cleanWord)) {
				// Only add if not already in entities
				found := false
				for _, e := range result.Entities {
					if strings.EqualFold(e, cleanWord) {
						found = true
						break
					}
				}
				if !found {
					result.Entities = append(result.Entities, cleanWord)
				}
			}
		}
	}

	// If we found entities but no specific intent, set intent to search
	if len(result.Entities) > 0 && result.Intent == "search" {
		result.Intent = "search"
	}

	// If we found categories or sources, prioritize those intents
	if len(result.Categories) > 0 {
		result.Intent = "category"
	} else if len(result.Sources) > 0 {
		result.Intent = "source"
	}

	return result, nil
}

// isCommonWord checks if a word is a common word that shouldn't be treated as an entity
func isCommonWord(word string) bool {
	commonWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "this": true,
		"that": true, "have": true, "from": true, "about": true, "what": true,
		"when": true, "where": true, "which": true,
	}
	return commonWords[strings.ToLower(word)]
}

// GenerateSummary generates a summary of an article using an LLM
func GenerateSummary(title, description string) (string, error) {
	// In a real implementation, this would call an actual LLM API
	// For now, we'll return a simple concatenation
	return fmt.Sprintf("%s. %s", title, description), nil
}

// InitializeLLMClient initializes the LLM client with the API key from environment variables
func InitializeLLMClient(apiKey string) {
	// In a real implementation, this would initialize the LLM client
	// For now, we'll just log that it was called
	log.Println("LLM client initialized")
}
