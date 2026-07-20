package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/seu-usuario/kafka-go/consumer/internal/service"
)

// metricsProvider é o mínimo que o controller precisa do agregador.
type metricsProvider interface {
	MetricsSnapshot() service.MetricsSnapshot
}

// AggregatorController expõe health e métricas do serviço de agregação.
type AggregatorController struct {
	svc metricsProvider
}

func NewAggregatorController(svc metricsProvider) *AggregatorController {
	return &AggregatorController{svc: svc}
}

func (c *AggregatorController) Health(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ok", "service": "aggregator"})
}

func (c *AggregatorController) Metrics(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, c.svc.MetricsSnapshot())
}
