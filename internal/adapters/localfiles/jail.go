package localfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func resolveInLibrary(root, rel string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("library root is required")
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("path must be relative")
	}
	if containsParentSegment(rel) {
		return "", fmt.Errorf("path must not contain .. segments")
	}

	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	cleanRel := filepath.Clean(rel)
	if cleanRel == "." {
		cleanRel = ""
	}
	joined := filepath.Clean(filepath.Join(cleanRoot, cleanRel))

	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", fmt.Errorf("resolve library root: %w", err)
	}
	realCandidate, err := evalExistingPrefix(cleanRoot, joined)
	if err != nil {
		return "", err
	}
	if !pathWithin(realRoot, realCandidate) {
		return "", fmt.Errorf("path escapes library root")
	}

	// Return the cleaned lexical path under the configured root, not the real
	// symlink-expanded path. Missing leaves cannot be fully resolved yet, and
	// callers must still open the joined path they validated to avoid swapping
	// path identities between validation and use.
	return joined, nil
}

func evalExistingPrefix(root, full string) (string, error) {
	root = filepath.Clean(root)
	full = filepath.Clean(full)
	cur := full
	var missing []string

	for {
		if !pathWithin(root, cur) {
			return "", fmt.Errorf("path escapes library root")
		}
		info, err := os.Lstat(cur)
		if err == nil {
			if len(missing) > 0 && !info.IsDir() {
				return "", fmt.Errorf("existing prefix is not a directory")
			}
			realPrefix, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			parts := append([]string{realPrefix}, missing...)
			return filepath.Join(parts...), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if sameCleanPath(cur, root) {
			return "", err
		}
		missing = append([]string{filepath.Base(cur)}, missing...)
		next := filepath.Dir(cur)
		if next == cur {
			return "", err
		}
		cur = next
	}
}

func containsParentSegment(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return true
		}
	}
	return false
}

func pathWithin(root, path string) bool {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	if samePath(cleanRoot, cleanPath) {
		return true
	}
	relRoot, relPath := cleanRoot, cleanPath
	if caseInsensitivePaths() {
		relRoot = strings.ToLower(relRoot)
		relPath = strings.ToLower(relPath)
	}
	rel, err := filepath.Rel(relRoot, relPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func sameCleanPath(a, b string) bool {
	return samePath(filepath.Clean(a), filepath.Clean(b))
}

func samePath(a, b string) bool {
	if caseInsensitivePaths() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func caseInsensitivePaths() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}
