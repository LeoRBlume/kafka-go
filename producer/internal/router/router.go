package router

import (
	"github.com/gin-gonic/gin"
	"github.com/seu-usuario/kafka-go/producer/internal/controller"
)

func SetupRouter(ctrl *controller.ProduceController) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.POST("/produce", ctrl.Produce)
	r.GET("/health", ctrl.Health)

	return r
}
