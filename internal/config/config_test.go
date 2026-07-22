package config

import (
	"reflect"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("APP_ENV", "")
	t.Setenv("HTTP_ADDRESS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://erp.example.com,https://erp.example.com, https://preview.example.com ")
	t.Setenv("DATABASE_CONNECT_TIMEOUT", "")
	t.Setenv("DATABASE_HEALTH_TIMEOUT", "")
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Environment != EnvironmentDevelopment {
		t.Fatalf("Environment = %q, want %q", cfg.Environment, EnvironmentDevelopment)
	}
	if cfg.HTTPAddress != ":8080" {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, ":8080")
	}
	if cfg.DatabaseConnectTimeout != 5*time.Second {
		t.Fatalf("DatabaseConnectTimeout = %s, want 5s", cfg.DatabaseConnectTimeout)
	}
	wantOrigins := []string{"https://erp.example.com", "https://preview.example.com"}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, wantOrigins) {
		t.Fatalf("CORSAllowedOrigins = %#v, want %#v", cfg.CORSAllowedOrigins, wantOrigins)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("SHUTDOWN_TIMEOUT", "later")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestLoadRejectsUnsafeCookieName(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("APP_SESSION_COOKIE_NAME", "bad;cookie")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestLoadValidatesCookieSameSite(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("APP_SESSION_COOKIE_SAME_SITE", "none")
	t.Setenv("APP_SESSION_COOKIE_SECURE", "false")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted SameSite=None without a Secure cookie")
	}

	t.Setenv("APP_SESSION_COOKIE_SECURE", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SessionCookieSameSite != "none" {
		t.Fatalf("SessionCookieSameSite = %q, want none", cfg.SessionCookieSameSite)
	}
}
