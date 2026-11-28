// Package guard holds a test that keeps the custom ent client independent of upstream's.
package guard

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Built from two parts so a plain grep for the import path over internal/custom/ent stays empty.
const upstreamEnt = "github.com/sysadminsmedia/homebox/backend/internal/data/" + "ent"

// The generator resolves the bare identifier `ent` through goimports, which can pick upstream's
// generated package (internal/data/ent) instead of entgo.io/ent. That silently couples the
// custom client to upstream's generated code (.fork/PLAN.md R2/R10). Fail if any file under
// internal/custom/ent imports upstream's ent package or any of its subpackages.
func TestXCustomEntDoesNotImportUpstreamEnt(t *testing.T) {
	root := ".." // internal/custom/ent
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == upstreamEnt || strings.HasPrefix(p, upstreamEnt+"/") {
				t.Errorf("%s imports upstream ent %q: regenerate with `go generate ./internal/custom/ent` and make sure client.go imports entgo.io/ent", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 {
		t.Fatalf("only %d files scanned; is the path right?", checked)
	}
}
