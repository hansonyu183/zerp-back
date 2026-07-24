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
	voudomain "github.com/hansonyu183/zerp-back/internal/domains/vou"
	"github.com/jackc/pgx/v5/pgxpool"
)

type databasePinger interface {
	Ping(context.Context) error
}

func New(cfg config.Config, db *pgxpool.Pool, logger *slog.Logger) (*gin.Engine, error) {
	appService := appdomain.NewService(db, cfg, logger)
	bobService := bobdomain.NewService(db)
	vouService, err := voudomain.NewService(db, bobService, voudomain.AttachmentOptions{
		Root: cfg.AttachmentStorageRoot, UploadTTL: cfg.AttachmentUploadTTL, DownloadTTL: cfg.AttachmentDownloadTTL,
	}, logger)
	if err != nil {
		return nil, err
	}
	return newRouter(cfg, db, logger, func(router *gin.Engine) {
		appdomain.NewHandler(appService, cfg, logger).Register(router)
		authorizer := appAuthorizer{service: appService, cfg: cfg}
		bobdomain.NewHandler(bobService, authorizer, logger).Register(router)
		voudomain.NewHandler(vouService, authorizer, logger).Register(router)
	}), nil
}

func newRouter(
	cfg config.Config,
	db databasePinger,
	logger *slog.Logger,
	registerBusinessRoutes func(*gin.Engine),
) *gin.Engine {
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
	if registerBusinessRoutes != nil {
		registerBusinessRoutes(router)
	}

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":     "route not found",
			"requestId": c.GetString("requestId"),
		})
	})

	return router
}
