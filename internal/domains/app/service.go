package app

import (
	"fmt"
	"log/slog"

	"github.com/hansonyu183/zerp-back/internal/config"
	dbsqlc "github.com/hansonyu183/zerp-back/internal/database/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool          *pgxpool.Pool
	queries       *dbsqlc.Queries
	cfg           config.Config
	logger        *slog.Logger
	dummyPassword string
}

func NewService(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) *Service {
	dummy, err := hashPassword("Dummy-login-password-1!")
	if err != nil {
		panic(fmt.Sprintf("initialize password verifier: %v", err))
	}
	return &Service{pool: pool, queries: dbsqlc.New(pool), cfg: cfg, logger: logger, dummyPassword: dummy}
}
