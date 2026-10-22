package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"PORT", "DB_PATH", "UPLOADS_DIR", "CLEANUP_AT", "DRAFT_MIN_AGE", "IMAGE_GRACE"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8080" || cfg.DBPath != "minipress.db" || cfg.UploadsDir != "uploads" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.CleanupHour != 23 || cfg.CleanupMinute != 30 {
		t.Errorf("default cleanup time should be 23:30, got %02d:%02d", cfg.CleanupHour, cfg.CleanupMinute)
	}
	if cfg.DraftMinAge != time.Hour || cfg.ImageGrace != 24*time.Hour {
		t.Errorf("unexpected durations: %v / %v", cfg.DraftMinAge, cfg.ImageGrace)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("PORT", "9000")
	t.Setenv("CLEANUP_AT", "04:05")
	t.Setenv("DRAFT_MIN_AGE", "90m")
	t.Setenv("IMAGE_GRACE", "48h")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "9000" || cfg.CleanupHour != 4 || cfg.CleanupMinute != 5 ||
		cfg.DraftMinAge != 90*time.Minute || cfg.ImageGrace != 48*time.Hour {
		t.Errorf("overrides were not applied: %+v", cfg)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"CLEANUP_AT":    "25:99",
		"DRAFT_MIN_AGE": "hour",
		"IMAGE_GRACE":   "-5h",
		"TRUST_PROXY":   "maybe",
	}
	for key, val := range cases {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, val)
			if _, err := Load(); err == nil {
				t.Errorf("%s=%q: expected an error", key, val)
			}
		})
	}
}
