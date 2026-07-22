package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hansonyu183/zerp-back/internal/api/middleware"
	"github.com/hansonyu183/zerp-back/internal/config"
	appdomain "github.com/hansonyu183/zerp-back/internal/domains/app"
	bobdomain "github.com/hansonyu183/zerp-back/internal/domains/bob"
	"github.com/jackc/pgx/v5/pgxpool"
)

type databasePinger interface {
	Ping(context.Context) error
}

func New(cfg config.Config, db databasePinger, logger *slog.Logger) *gin.Engine {
	if cfg.Environment == config.EnvironmentProduction {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(
		middleware.RequestID(),
		middleware.RequestLogger(logger),
		middleware.Recovery(logger),
		middleware.CORS(cfg.CORSAllowedOrigins),
	)

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", readinessHandler(db, cfg.DatabaseHealthTimeout))
	if pool, ok := db.(*pgxpool.Pool); ok {
		appService := appdomain.NewService(pool, cfg, logger)
		appdomain.NewHandler(appService, cfg, logger).Register(router)
		bobdomain.NewHandler(bobdomain.NewService(pool, logger), appAuthorizer{service: appService, cfg: cfg}, logger).Register(router)
	}

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":     "route not found",
			"requestId": c.GetString("requestId"),
		})
	})

	return router
}
