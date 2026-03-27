package router

import (
	"github.com/gin-gonic/gin"
	"github.com/seu-usuario/kafka-go/consumer/internal/controller"
)

func SetupRouter(ctrl *controller.HealthController) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.GET("/health", ctrl.Health)

	return r
}
