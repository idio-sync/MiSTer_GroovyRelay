// Package eventlog is a small in-memory ring buffer for adapter and
// core lifecycle events surfaced in the Status home and Diagnostics
// pages. See docs/specs/2026-04-26-ui-redesign-design.md §8.4.
//
// This package is a leaf utility: it depends on nothing else in this
// repo and is consumed by core, adapters, and ui. Do not import any
// internal package from here.
package eventlog

import (
	"sync"
	"time"
)

// Severity ranks an entry. Render color in the UI keys off this.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityErr
)

// String returns a stable lowercase name. UI templates depend on these
// exact strings — do not rename.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarn:
		return "warn"
	case SeverityErr:
		return "err"
	default:
		return "unknown"
	}
}

// Entry is one ring-buffer record. Source is a short layer/component
// label (e.g. "core", "bridge", or an adapter name). Per spec §8.4
// each event class has exactly one writer; do not dual-write the
// same Source from multiple goroutines.
type Entry struct {
	Time     time.Time
	Severity Severity
	Source   string
	Message  string
}

// Log is a thread-safe ring buffer of recent events. Append is O(1);
// Snapshot returns a copy in chronological order (oldest first).
//
// Capacity is fixed at construction; once full, oldest entries are
// evicted on Append. Per spec §8.4, the recommended capacity is 256
// for production use.
type Log struct {
	mu  sync.Mutex
	buf []Entry
	// next is the index where the next Append will write. When the
	// buffer is full, next wraps modulo len(buf).
	next int
	// full is true once we have wrapped at least once.
	full bool
}

// New constructs a Log with the given capacity. Capacity must be >0.
func New(capacity int) *Log {
	if capacity <= 0 {
		panic("eventlog: capacity must be > 0")
	}
	return &Log{buf: make([]Entry, capacity)}
}

// Append records one entry. Safe for concurrent callers.
func (l *Log) Append(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf[l.next] = e
	l.next++
	if l.next == len(l.buf) {
		l.next = 0
		l.full = true
	}
}

// Snapshot returns all current entries in chronological order
// (oldest first). The returned slice is independent of the buffer —
// callers may mutate it without affecting the log.
func (l *Log) Snapshot() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.full {
		out := make([]Entry, l.next)
		copy(out, l.buf[:l.next])
		return out
	}
	out := make([]Entry, len(l.buf))
	copy(out, l.buf[l.next:])
	copy(out[len(l.buf)-l.next:], l.buf[:l.next])
	return out
}
