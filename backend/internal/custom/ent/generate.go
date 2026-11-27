// Package ent is the generated ent client for the fork's custom tables.
//
// Regenerate with `go generate ./internal/custom/ent` from backend/, then run
// `git checkout -- go.mod go.sum` if the generator touched them (.fork/PLAN.md R6):
// the generated code only needs entgo.io/ent, which upstream already requires.
package ent

//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate ./schema
