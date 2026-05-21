package chassis

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionImports_NoCrossPackageCoupling(t *testing.T) {
	t.Parallel()

	const modulePath = "github.com/idio-sync/MiSTer_GroovyRelay"
	repoRoot := repoRootFromWD(t)

	rules := []struct {
		fromPkg   string
		fromDir   string
		forbidden []string
	}{
		{
			fromPkg: modulePath + "/internal/chassis",
			fromDir: filepath.Join(repoRoot, "internal", "chassis"),
			forbidden: []string{
				modulePath + "/internal/ui",
				modulePath + "/internal/uiserver",
			},
		},
		{
			fromPkg: modulePath + "/internal/ui",
			fromDir: filepath.Join(repoRoot, "internal", "ui"),
			forbidden: []string{
				modulePath + "/internal/chassis",
			},
		},
		{
			fromPkg: modulePath + "/internal/uiserver",
			fromDir: filepath.Join(repoRoot, "internal", "uiserver"),
			forbidden: []string{
				modulePath + "/internal/chassis",
			},
		},
	}

	for _, rule := range rules {
		rule := rule
		t.Run(rule.fromPkg, func(t *testing.T) {
			err := filepath.WalkDir(rule.fromDir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}

				fset := token.NewFileSet()
				file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
				if err != nil {
					return err
				}

				for _, imp := range file.Imports {
					importPath := strings.Trim(imp.Path.Value, `"`)
					for _, forbidden := range rule.forbidden {
						if importPath == forbidden {
							pos := fset.Position(imp.Pos())
							t.Errorf("forbidden production import: %s -> %s at %s",
								rule.fromPkg, importPath, pos)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("scan %s: %v", rule.fromDir, err)
			}
		})
	}
}

func repoRootFromWD(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("could not find repo root from %s", wd)
		}
		wd = parent
	}
}
