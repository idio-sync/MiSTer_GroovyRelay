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
// block inside doc. It removes the parent table and descendant adapter
// subtables before inserting the normalized replacement at the first
// removed location. Descendant removal matters for adapters that expose
// dynamic dotted keys through the generic UI: leaving old subtables behind
// can make the next TOML parse fail with duplicate definitions.
func replaceAdapterSection(doc []byte, name string, section []byte) []byte {
	section = bytes.TrimRight(section, "\r\n\t ")
	section = append(section, '\n')

	header := fmt.Sprintf("[adapters.%s]", name)
	descendantPrefix := fmt.Sprintf("[adapters.%s.", name)
	lines := strings.Split(string(doc), "\n")

	outLines := make([]string, 0, len(lines))
	inserted := false
	removedAny := false

	for i := 0; i < len(lines); {
		tr := strings.TrimSpace(lines[i])
		if tr == header || (strings.HasPrefix(tr, descendantPrefix) && strings.HasSuffix(tr, "]")) {
			if !inserted {
				outLines = append(outLines, header)
				outLines = append(outLines, strings.Split(strings.TrimRight(string(section), "\n"), "\n")...)
				inserted = true
			}
			removedAny = true
			i++
			for i < len(lines) {
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, "[") && strings.HasSuffix(next, "]") {
					break
				}
				i++
			}
			continue
		}
		outLines = append(outLines, lines[i])
		i++
	}

	if !removedAny {
		// Append. Ensure doc ends with a newline before concatenating.
		out := strings.TrimRight(string(doc), "\r\n\t ") + "\n\n"
		out += header + "\n" + string(section)
		return []byte(out)
	}
	return []byte(strings.Join(outLines, "\n"))
}
