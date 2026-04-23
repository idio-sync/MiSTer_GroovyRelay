package uiserver

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// AdapterSaver replaces the [adapters.<name>] section of the on-disk
// config.toml with a new TOML snippet. Uses a line-level rewrite
// (replaceAdapterSection) rather than re-encoding the whole Sectioned
// — BurntSushi's encoder doesn't round-trip toml.Primitive values
// faithfully, so a full re-encode would lose adapter sections the UI
// doesn't currently touch.
type AdapterSaver struct {
	path string
	mu   *sync.Mutex // shared with BridgeSaver for same-file serialization
}

// NewAdapterSaver constructs an AdapterSaver that rewrites the given
// config path. Pass BridgeSaver.Mu() as the mutex so bridge + adapter
// saves serialize against each other; both paths read-modify-write the
// same file.
func NewAdapterSaver(path string, mu *sync.Mutex) *AdapterSaver {
	return &AdapterSaver{path: path, mu: mu}
}

func (r *AdapterSaver) Save(name string, rawTOMLSection []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	updated := replaceAdapterSection(data, name, rawTOMLSection)
	return config.WriteAtomic(r.path, updated)
}

// replaceAdapterSection rewrites (or appends) the [adapters.<name>]
// block inside doc. The section is matched by exact header line; its
// body extends to the next [header] line or EOF. The replacement
// section is normalized to end with exactly one newline before
// splicing so repeated saves don't accumulate blank lines or run
// adjacent lines together.
func replaceAdapterSection(doc []byte, name string, section []byte) []byte {
	section = bytes.TrimRight(section, "\r\n\t ")
	section = append(section, '\n')

	header := fmt.Sprintf("[adapters.%s]", name)
	lines := strings.Split(string(doc), "\n")

	start := -1
	for i, ln := range lines {
		if strings.TrimSpace(ln) == header {
			start = i
			break
		}
	}

	if start < 0 {
		// Append. Ensure doc ends with a newline before concatenating.
		out := strings.TrimRight(string(doc), "\r\n\t ") + "\n\n"
		out += header + "\n" + string(section)
		return []byte(out)
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		tr := strings.TrimSpace(lines[i])
		if strings.HasPrefix(tr, "[") && strings.HasSuffix(tr, "]") {
			end = i
			break
		}
	}

	newLines := append([]string{}, lines[:start+1]...)
	sectionLines := strings.Split(strings.TrimRight(string(section), "\n"), "\n")
	newLines = append(newLines, sectionLines...)
	if end < len(lines) {
		newLines = append(newLines, "")
	}
	newLines = append(newLines, lines[end:]...)
	return []byte(strings.Join(newLines, "\n"))
}
