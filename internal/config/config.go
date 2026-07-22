package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	EnvironmentDevelopment = "development"
	EnvironmentTest        = "test"
	EnvironmentProduction  = "production"
)

type Config struct {
	Environment            string
	HTTPAddress            string
	DatabaseURL            string
	CORSAllowedOrigins     []string
	DatabaseConnectTimeout time.Duration
	DatabaseHealthTimeout  time.Duration
	ReadHeaderTimeout      time.Duration
	ShutdownTimeout        time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Environment:        valueOrDefault("APP_ENV", EnvironmentDevelopment),
		HTTPAddress:        valueOrDefault("HTTP_ADDRESS", ":8080"),
		DatabaseURL:        strings.TrimSpace(os.Getenv("DATABASE_URL")),
		CORSAllowedOrigins: splitAndTrim(os.Getenv("CORS_ALLOWED_ORIGINS")),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	if cfg.Environment != EnvironmentDevelopment && cfg.Environment != EnvironmentTest && cfg.Environment != EnvironmentProduction {
		return Config{}, fmt.Errorf("APP_ENV must be one of %q, %q, or %q", EnvironmentDevelopment, EnvironmentTest, EnvironmentProduction)
	}

	var err error
	if cfg.DatabaseConnectTimeout, err = durationOrDefault("DATABASE_CONNECT_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.DatabaseHealthTimeout, err = durationOrDefault("DATABASE_HEALTH_TIMEOUT", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ReadHeaderTimeout, err = durationOrDefault("HTTP_READ_HEADER_TIMEOUT", 5*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationOrDefault("SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func valueOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationOrDefault(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return duration, nil
}

func splitAndTrim(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}
