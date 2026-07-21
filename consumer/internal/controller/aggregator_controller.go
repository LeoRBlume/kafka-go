package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/seu-usuario/kafka-go/consumer/internal/service"
)

// observer é o mínimo que o controller precisa do agregador.
type observer interface {
	Observe() service.Observation
}

// AggregatorController expõe health e métricas do serviço de agregação.
type AggregatorController struct {
	svc observer
}

func NewAggregatorController(svc observer) *AggregatorController {
	return &AggregatorController{svc: svc}
}

func (c *AggregatorController) Health(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ok", "service": "aggregator"})
}

// Metrics expõe os contadores e gauges no text exposition format do Prometheus.
func (c *AggregatorController) Metrics(ctx *gin.Context) {
	ctx.Header("Content-Type", prometheusContentType)
	ctx.String(http.StatusOK, renderPrometheus(c.svc.Observe()))
}
