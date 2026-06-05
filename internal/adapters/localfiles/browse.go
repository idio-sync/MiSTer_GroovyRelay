package localfiles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const probeTimeout = 800 * time.Millisecond

type BrowseEntry struct {
	Name      string
	Rel       string
	IsDir     bool
	Playable  bool
	DurationS float64
	AudioOnly bool
}

func (a *Adapter) Browse(libName, rel string) ([]BrowseEntry, error) {
	return a.BrowseContext(context.Background(), libName, rel)
}

func (a *Adapter) BrowseContext(ctx context.Context, libName, rel string) ([]BrowseEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !a.IsEnabled() {
		return nil, fmt.Errorf("localfiles adapter is disabled")
	}
	lib, ok := a.libraryByName(libName)
	if !ok {
		return nil, fmt.Errorf("unknown library %q", libName)
	}
	dir, err := resolveInLibrary(lib.Root, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	out := make([]BrowseEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		childRel := joinRel(rel, name)
		childPath, err := resolveInLibrary(lib.Root, childRel)
		if err != nil {
			continue
		}

		isDir := entry.IsDir()
		playable := !isDir && isPlayable(name)
		if !isDir && !playable {
			continue
		}

		item := BrowseEntry{
			Name:     name,
			Rel:      filepath.ToSlash(filepath.Clean(childRel)),
			IsDir:    isDir,
			Playable: playable,
		}
		if playable {
			a.describeBrowseEntry(ctx, &item, childPath)
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		li, lj := lower(out[i].Name), lower(out[j].Name)
		if li == lj {
			return out[i].Name < out[j].Name
		}
		return li < lj
	})
	return out, nil
}

func (a *Adapter) describeBrowseEntry(ctx context.Context, entry *BrowseEntry, url string) {
	probe := a.probe
	if probe == nil {
		probe = a.probeDefault
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	result, err := probe(ctx, url, localFilePolicy())
	if err != nil || result == nil {
		return
	}
	entry.DurationS = result.Duration
	entry.AudioOnly = result.AudioRate > 0 && result.Width == 0
}

func (a *Adapter) libraryRoot(libName string) (string, bool) {
	lib, ok := a.libraryByName(libName)
	return lib.Root, ok
}

func (a *Adapter) libraryByName(libName string) (Library, bool) {
	cfg := a.configSnapshot()
	want := lower(strings.TrimSpace(libName))
	for _, lib := range cfg.Libraries {
		if lower(strings.TrimSpace(lib.Name)) == want {
			return lib, true
		}
	}
	return Library{}, false
}

func joinRel(parent, name string) string {
	parent = filepath.Clean(parent)
	if parent == "." {
		parent = ""
	}
	if parent == "" {
		return name
	}
	return filepath.Join(parent, name)
}
