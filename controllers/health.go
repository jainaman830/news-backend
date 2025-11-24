package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthCheckResponse represents the health check response structure
type HealthCheckResponse struct {
	Status   string `json:"status"`
	Database string `json:"database,omitempty"`
}

// HealthCheck handles the health check endpoint
func HealthCheck(c *gin.Context) {
	dbStatus := "disconnected"
	if mongoClient != nil {
		err := mongoClient.Ping(c, nil)
		if err == nil {
			dbStatus = "connected"
		}
	}

	status := http.StatusOK
	response := HealthCheckResponse{
		Status:   "ok",
		Database: dbStatus,
	}

	if dbStatus != "connected" {
		status = http.StatusServiceUnavailable
		response.Status = "unavailable"
	}

	c.JSON(status, response)
}
