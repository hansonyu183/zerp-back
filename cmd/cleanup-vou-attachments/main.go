package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/hansonyu183/zerp-back/internal/config"
	"github.com/hansonyu183/zerp-back/internal/database"
	bobdomain "github.com/hansonyu183/zerp-back/internal/domains/bob"
	voudomain "github.com/hansonyu183/zerp-back/internal/domains/vou"
	"github.com/hansonyu183/zerp-back/internal/platform/txevent"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	pool, err := database.Open(context.Background(), cfg.DatabaseURL, cfg.DatabaseConnectTimeout)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	service, err := voudomain.NewService(pool, bobdomain.NewService(pool), txevent.NewBus(), voudomain.AttachmentOptions{
		Root: cfg.AttachmentStorageRoot, UploadTTL: cfg.AttachmentUploadTTL, DownloadTTL: cfg.AttachmentDownloadTTL,
	}, logger)
	if err != nil {
		logger.Error("initialize attachment storage", "error", err)
		os.Exit(1)
	}
	removed, err := service.CleanupAttachments(context.Background(), 500)
	if err != nil {
		logger.Error("cleanup VOU attachments", "error", err)
		os.Exit(1)
	}
	logger.Info("VOU attachment cleanup completed", "removed", removed)
}
