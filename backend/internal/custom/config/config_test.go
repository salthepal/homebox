package config

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestXConfigDefaults(t *testing.T) {
	c, err := load(env(nil))
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, c.GcalSyncInterval)
	assert.False(t, c.GcalEnabled())
	assert.Len(t, c.GcalMissing(), 4)
}

func TestXConfigFull(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	c, err := load(env(map[string]string{
		"HBOX_X_PUBLIC_BASE_URL":      "https://hb.example.org/",
		"HBOX_X_GOOGLE_CLIENT_ID":     "id",
		"HBOX_X_GOOGLE_CLIENT_SECRET": "secret",
		"HBOX_X_SECRET_KEY":           key,
		"HBOX_X_GCAL_SYNC_INTERVAL":   "5m",
	}))
	require.NoError(t, err)
	assert.Equal(t, "https://hb.example.org", c.PublicBaseURL)
	assert.Equal(t, 5*time.Minute, c.GcalSyncInterval)
	assert.True(t, c.GcalEnabled())
}

func TestXConfigInvalid(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"key not base64":   {"HBOX_X_SECRET_KEY": "%%%"},
		"key wrong size":   {"HBOX_X_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 16))},
		"bad interval":     {"HBOX_X_GCAL_SYNC_INTERVAL": "soon"},
		"interval too low": {"HBOX_X_GCAL_SYNC_INTERVAL": "10s"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(env(m))
			assert.Error(t, err)
		})
	}
}
