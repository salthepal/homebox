// Package config loads settings for the fork's custom features from HBOX_X_* environment
// variables. It is deliberately independent of upstream's internal/sys/config so upstream
// config changes never conflict with ours (.fork/PLAN.md R5).
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"
)

const envPrefix = "HBOX_X_"

// Config holds custom-feature settings. Zero values mean "not configured".
type Config struct {
	// PublicBaseURL is the externally reachable base URL (no trailing slash), used for OAuth
	// redirects and deep links in calendar events.
	PublicBaseURL string
	// GoogleClientID / GoogleClientSecret identify the user's own Google OAuth client.
	GoogleClientID     string
	GoogleClientSecret string
	// SecretKey is a 32-byte key (HBOX_X_SECRET_KEY, base64) for encrypting tokens at rest
	// and signing OAuth state.
	SecretKey []byte
	// GcalSyncInterval is how often the calendar reconciler runs.
	GcalSyncInterval time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) { return load(os.Getenv) }

func load(getenv func(string) string) (Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(envPrefix + k)) }

	c := Config{
		PublicBaseURL:      strings.TrimRight(get("PUBLIC_BASE_URL"), "/"),
		GoogleClientID:     get("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: get("GOOGLE_CLIENT_SECRET"),
		GcalSyncInterval:   15 * time.Minute,
	}

	if v := get("SECRET_KEY"); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return Config{}, fmt.Errorf("%sSECRET_KEY: not valid base64: %w", envPrefix, err)
		}
		if len(key) != 32 {
			return Config{}, fmt.Errorf("%sSECRET_KEY: must decode to 32 bytes, got %d", envPrefix, len(key))
		}
		c.SecretKey = key
	}

	if v := get("GCAL_SYNC_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("%sGCAL_SYNC_INTERVAL: %w", envPrefix, err)
		}
		if d < time.Minute {
			return Config{}, fmt.Errorf("%sGCAL_SYNC_INTERVAL: must be at least 1m, got %s", envPrefix, d)
		}
		c.GcalSyncInterval = d
	}

	return c, nil
}

// GcalMissing lists the env vars still needed for Google Calendar sync (empty = ready).
func (c Config) GcalMissing() []string {
	var missing []string
	if c.PublicBaseURL == "" {
		missing = append(missing, envPrefix+"PUBLIC_BASE_URL")
	}
	if c.GoogleClientID == "" {
		missing = append(missing, envPrefix+"GOOGLE_CLIENT_ID")
	}
	if c.GoogleClientSecret == "" {
		missing = append(missing, envPrefix+"GOOGLE_CLIENT_SECRET")
	}
	if len(c.SecretKey) == 0 {
		missing = append(missing, envPrefix+"SECRET_KEY")
	}
	return missing
}

// GcalEnabled reports whether everything needed for Google Calendar sync is configured.
func (c Config) GcalEnabled() bool { return len(c.GcalMissing()) == 0 }
