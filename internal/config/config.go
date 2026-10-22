package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Port       string
	DBPath     string
	UploadsDir string

	TrustProxy bool

	CleanupHour   int
	CleanupMinute int

	DraftMinAge time.Duration

	ImageGrace time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:        getenv("PORT", "8080"),
		DBPath:      getenv("DB_PATH", "minipress.db"),
		UploadsDir:  getenv("UPLOADS_DIR", "uploads"),
		DraftMinAge: time.Hour,
		ImageGrace:  24 * time.Hour,
	}

	var err error
	switch strings.ToLower(getenv("TRUST_PROXY", "false")) {
	case "1", "true", "yes":
		cfg.TrustProxy = true
	case "0", "false", "no":
	default:
		return nil, fmt.Errorf("TRUST_PROXY: expected true or false")
	}
	if cfg.CleanupHour, cfg.CleanupMinute, err = parseClock(getenv("CLEANUP_AT", "23:30")); err != nil {
		return nil, fmt.Errorf("CLEANUP_AT: %w", err)
	}
	if cfg.DraftMinAge, err = parseDuration("DRAFT_MIN_AGE", cfg.DraftMinAge); err != nil {
		return nil, err
	}
	if cfg.ImageGrace, err = parseDuration("IMAGE_GRACE", cfg.ImageGrace); err != nil {
		return nil, err
	}
	return cfg, nil
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseDuration(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s: expected a duration like 90m or 24h, got %q", key, raw)
	}
	return d, nil
}

func parseClock(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	return t.Hour(), t.Minute(), nil
}
