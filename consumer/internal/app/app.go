package app

import (
	"context"
	"net/http"
	"time"

	"github.com/LeoRBlume/go-libs/logger"
	"github.com/seu-usuario/kafka-go/consumer/config"
	"github.com/seu-usuario/kafka-go/consumer/internal/controller"
	"github.com/seu-usuario/kafka-go/consumer/internal/router"
	"github.com/seu-usuario/kafka-go/consumer/internal/service"
)

const shutdownTimeout = 10 * time.Second

// Run monta as dependências do agregador, sobe o loop de consumo e o servidor
// HTTP (health/metrics) e faz shutdown gracioso quando ctx é cancelado.
func Run(ctx context.Context) error {
	cfg := config.NewDefaultConfig()

	logger.Infof(ctx, "app.Run", "starting aggregator on port %s (window=%s grace=%s output=%s)",
		cfg.Port, cfg.WindowSize, cfg.GracePeriod, cfg.OutputTopic)

	svc, err := service.NewAggregatorService(cfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	go func() {
		if err := svc.Start(ctx); err != nil {
			logger.Error(ctx, "app.Run", "aggregator stopped with error", err)
		}
	}()

	ctrl := controller.NewAggregatorController(svc)
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router.SetupRouter(ctrl),
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(ctx, "app.Run", "server error", err)
		}
	}()

	logger.Infof(ctx, "app.Run", "aggregator running on port %s", cfg.Port)
	<-ctx.Done()

	logger.Info(ctx, "app.Run", "shutting down aggregator")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
