package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xapi "github.com/sysadminsmedia/homebox/backend/internal/custom/api"
)

// Regression tests for the review findings on the Projects service/API [P2].
func TestCustomProjectsReviewFollowUp(t *testing.T) {
	e := newProjectsEnv(t)
	item := e.createItem(e.gidA, "Router")
	p := e.createProject(e.tokA, map[string]any{"name": "P"})
	stepPath := func(sid uuid.UUID, suffix string) string {
		return fmt.Sprintf("/projects/%s/steps/%s%s", p.ID, sid, suffix)
	}

	// Step "done" is only reachable through /complete (a logged step would skip its entry).
	logged := e.addStep(e.tokA, p.ID, map[string]any{jTitle: "Flash", jEntityID: item, jLogAsMaint: true})
	rec := e.do(http.MethodPut, stepPath(logged.ID, ""), e.tokA,
		map[string]any{jTitle: "Flash", jStatus: "done", jEntityID: item, jLogAsMaint: true})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "/complete")
	rec = e.do(http.MethodPost, fmt.Sprintf("/projects/%s/steps", p.ID), e.tokA,
		map[string]any{jTitle: "born done", jStatus: "done"})
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Empty(t, e.maintenance(e.gidA, item))

	// Complete twice: one entry, then 409. Leaving done through PUT still works.
	rec = e.do(http.MethodPost, stepPath(logged.ID, "/complete"), e.tokA, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = e.do(http.MethodPost, stepPath(logged.ID, "/complete"), e.tokA, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Len(t, e.maintenance(e.gidA, item), 1)
	rec = e.do(http.MethodPut, stepPath(logged.ID, ""), e.tokA,
		map[string]any{jTitle: "Flash", jStatus: "todo", jEntityID: item, jLogAsMaint: true})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "todo", decodeInto[xapi.StepDTO](t, rec).Status)

	// Trailing data after the JSON value is a 400 and creates nothing.
	for _, body := range []string{`{"name":"T"}{"x":1}`, `{"name":"T"} garbage`, `{"name":"T"}{"name":"U"}`} {
		rec = e.do(http.MethodPost, pathProjects, e.tokA, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
	}
	rec = e.do(http.MethodPost, pathProjects, e.tokA, "{\"name\":\"T\"}\n  ")
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// Text length (bytes, as ent counts) and money rules give 400, never 500.
	long := strings.Repeat("a", 256)
	twoByte := strings.Repeat("é", 128)
	ok := e.addStep(e.tokA, p.ID, map[string]any{jTitle: strings.Repeat("a", 255), jEstCost: 80.55})
	for _, tc := range []struct {
		name, method, path string
		body               map[string]any
	}{
		{"long name", http.MethodPost, pathProjects, map[string]any{"name": long}},
		{"multibyte name", http.MethodPost, pathProjects, map[string]any{"name": twoByte}},
		{"long name update", http.MethodPut, "/projects/" + p.ID.String(), map[string]any{"name": long}},
		{"long title", http.MethodPost, "/projects/" + p.ID.String() + "/steps", map[string]any{jTitle: long}},
		{"multibyte title update", http.MethodPut, stepPath(ok.ID, ""), map[string]any{jTitle: twoByte}},
		{"budget 3 decimals", http.MethodPost, pathProjects, map[string]any{"name": "x", "budget": 80.555}},
		{"budget 1e12", http.MethodPost, pathProjects, map[string]any{"name": "x", "budget": 1e12}},
		{"est cost 3 decimals", http.MethodPut, stepPath(ok.ID, ""), map[string]any{jTitle: "x", jEstCost: 80.555}},
		{"actual cost 1e12", http.MethodPut, stepPath(ok.ID, ""), map[string]any{jTitle: "x", "actual_cost": 1e12}},
		{"complete cost 3 decimals", http.MethodPost, stepPath(ok.ID, "/complete"), map[string]any{"actual_cost": 80.555}},
	} {
		rec = e.do(tc.method, tc.path, e.tokA, tc.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", tc.name, rec.Body.String())
	}
	for _, v := range []float64{80, 80.5, 80.55} {
		rec = e.do(http.MethodPost, pathProjects, e.tokA, map[string]any{"name": "money", "budget": v})
		assert.Equal(t, http.StatusCreated, rec.Code, "%v: %s", v, rec.Body.String())
	}
	got := e.getProject(e.tokA, p.ID)
	assert.Equal(t, "Flash", got.Steps[0].Title, "rejected updates changed nothing")
	assert.Equal(t, strings.Repeat("a", 255), got.Steps[1].Title)
}
