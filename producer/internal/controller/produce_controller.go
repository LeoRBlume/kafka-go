package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/seu-usuario/kafka-go/producer/internal/ports"
)

type ProduceController struct {
	svc ports.ProducerPort
}

func NewProduceController(svc ports.ProducerPort) *ProduceController {
	return &ProduceController{svc: svc}
}

type produceRequest struct {
	Count int `json:"count"`
}

func (c *ProduceController) Produce(ctx *gin.Context) {
	var req produceRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Count <= 0 || req.Count > 1_000_000 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "count must be between 1 and 1000000"})
		return
	}

	result, err := c.svc.Produce(ctx.Request.Context(), req.Count)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, result)
}

func (c *ProduceController) Health(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ok", "service": "producer"})
}
