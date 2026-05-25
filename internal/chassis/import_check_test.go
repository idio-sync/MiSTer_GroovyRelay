package chassis

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
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
				modulePath + "/internal/adapters/auxadapter",
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
		{
			fromPkg: modulePath + "/internal/playback",
			fromDir: filepath.Join(repoRoot, "internal", "playback"),
			forbidden: []string{
				modulePath + "/internal/chassis",
				modulePath + "/internal/ui",
				modulePath + "/internal/uiserver",
			},
		},
		{
			fromPkg: modulePath + "/internal/core",
			fromDir: filepath.Join(repoRoot, "internal", "core"),
			forbidden: []string{
				modulePath + "/internal/adapters",
				modulePath + "/internal/chassis",
				modulePath + "/internal/playback",
				modulePath + "/internal/ui",
				modulePath + "/internal/uiserver",
			},
		},
		{
			fromPkg: modulePath + "/internal/adapters",
			fromDir: filepath.Join(repoRoot, "internal", "adapters"),
			forbidden: []string{
				modulePath + "/internal/chassis",
				modulePath + "/internal/playback",
				modulePath + "/internal/ui",
				modulePath + "/internal/uiserver",
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
					importPath, err := importPathFromLiteral(imp.Path.Value)
					if err != nil {
						pos := fset.Position(imp.Pos())
						return fmt.Errorf("decode import literal %s at %s: %w", imp.Path.Value, pos, err)
					}
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

func TestProductionImports_DecodesRawStringImportLiterals(t *testing.T) {
	t.Parallel()

	const source = "package sample\n\nimport `github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis`\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "raw_import.go", source, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(file.Imports) != 1 {
		t.Fatalf("imports = %d, want 1", len(file.Imports))
	}

	got, err := importPathFromLiteral(file.Imports[0].Path.Value)
	if err != nil {
		t.Fatalf("importPathFromLiteral: %v", err)
	}
	const want = "github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	if got != want {
		t.Fatalf("import path = %q, want %q", got, want)
	}
}

func importPathFromLiteral(literal string) (string, error) {
	return strconv.Unquote(literal)
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
