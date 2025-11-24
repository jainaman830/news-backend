package routes

import (
	"news-backend/controllers"

	"github.com/gin-gonic/gin"
)

func SetupRoutes(router *gin.Engine) {
	group := router.Group("/api/v1/news")
	{
		group.GET("/category", controllers.GetArticlesByCategory)
		group.GET("/score", controllers.GetArticlesByScore)
		group.GET("/search", controllers.SearchArticles)
		group.GET("/source", controllers.GetArticlesBySource)
		group.GET("/nearby", controllers.GetNearbyArticles)
		group.GET("/trending", controllers.GetTrending)
		group.GET("/process", controllers.ProcessQuery)
	}
}
