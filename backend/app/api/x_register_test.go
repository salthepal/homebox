package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
	xent "github.com/sysadminsmedia/homebox/backend/internal/custom/ent"
	xmigrations "github.com/sysadminsmedia/homebox/backend/internal/custom/migrations"
	"github.com/sysadminsmedia/homebox/backend/internal/custom/xtest"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

// newCustomTestApp builds an app on a real migrated database (SQLite, or Postgres with
// TEST_X_DB=postgres) with one logged-in user, plus the custom deps.
func newCustomTestApp(t *testing.T) (*app, string, uuid.UUID, xapi.Deps) {
	t.Helper()
	ctx := context.Background()

	d := xtest.Open(t)
	xtest.UpstreamMigrate(t, d)
	_, err := xmigrations.Run(ctx, d.SQL, d.Driver)
	require.NoError(t, err)

	client := ent.NewClient(ent.Driver(entsql.OpenDB(d.Dialect, d.SQL)))
	repos := repo.New(client, eventbus.New(), config.Storage{
		PrefixPath: "/",
		ConnString: "file://" + os.TempDir(),
	}, "mem://{{ .Topic }}", config.Thumbnail{}, nil)

	group, err := repos.Groups.GroupCreate(ctx, "x-test", uuid.Nil)
	require.NoError(t, err)

	const email, password = "x-test@example.com", "correct horse battery staple"
	hash, err := hasher.HashPassword(password)
	require.NoError(t, err)
	_, err = repos.Users.Create(ctx, repo.UserCreate{
		Name: "X Test", Email: email, Password: &hash, DefaultGroupID: group.ID,
	})
	require.NoError(t, err)

	svc := services.New(repos)
	tokens, err := svc.User.Login(ctx, email, password, false)
	require.NoError(t, err)

	a := &app{conf: &config.Config{}, db: client, repos: repos, services: svc}
	deps := xapi.Deps{Ent: xent.NewClient(xent.Driver(entsql.OpenDB(d.Dialect, d.SQL)))}
	return a, tokens.Raw, group.ID, deps
}

func TestCustomRoutesMountAlongsideUpstream(t *testing.T) {
	a, token, gid, deps := newCustomTestApp(t)

	r := chi.NewRouter()
	chain := errchain.New(mid.Errors(zerolog.Nop()))
	// Mirrors routes.go: upstream mounts its own sub-router at /api/v1 first.
	r.Route(prefix+"/v1", func(r chi.Router) {
		r.Get("/status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	mountCustomRoutes(a, r, chain, deps)

	do := func(path, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	t.Run("upstream route still resolves", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, do("/api/v1/status", "").Code)
	})

	t.Run("custom route requires auth", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, do("/api/v1/x/ping", "").Code)
	})

	t.Run("custom route with auth", func(t *testing.T) {
		rec := do("/api/v1/x/ping", token)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body xapi.PingResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.True(t, body.OK)
		assert.Equal(t, gid, body.GroupID)
		assert.Equal(t, "custom", body.Schema)
	})
}
