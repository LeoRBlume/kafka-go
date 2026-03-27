package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type HealthController struct{}

func NewHealthController() *HealthController {
	return &HealthController{}
}

func (c *HealthController) Health(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ok", "service": "consumer"})
}
