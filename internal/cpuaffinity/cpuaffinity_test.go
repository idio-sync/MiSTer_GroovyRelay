package cpuaffinity

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseCPUList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"\n", nil},
		{"3", []int{3}},
		{"6-15,22-31\n", []int{6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}},
		{"0,2-3,7", []int{0, 2, 3, 7}},
		{"7,6", []int{6, 7}},
		{"1-2,2-3", []int{1, 2, 3}},
	}
	for _, c := range cases {
		got, err := ParseCPUList(c.in)
		if err != nil {
			t.Fatalf("ParseCPUList(%q): %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseCPUList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseCPUListRejectsMalformed(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"a", "3-", "-3", "5-2", "1,,2", "1-2-3"} {
		if _, err := ParseCPUList(in); err == nil {
			t.Errorf("ParseCPUList(%q): want error", in)
		}
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	isolated := []int{6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}
	cases := []struct {
		name     string
		allowed  []int
		isolated []int
		online   int
		warn     bool
		contains []string
	}{
		{name: "unpinned, no isolation", allowed: seq(0, 31), online: 32},
		// Unpinned on an isolcpus host: the kernel balances the
		// non-isolated cores, so the relay is fine.
		{name: "unpinned with isolation", allowed: seq(0, 31), isolated: isolated, online: 32},
		{name: "pinned to non-isolated", allowed: []int{0, 1, 16, 17}, isolated: isolated, online: 32},
		// One isolated core among ordinary ones still balances.
		{name: "mixed pinning", allowed: []int{5, 6}, isolated: isolated, online: 32},
		{name: "pinned to isolated", allowed: []int{6, 7, 22, 23}, isolated: isolated, online: 32, warn: true,
			contains: []string{"6,7,22,23", "isolated", "CPU pinning"}},
		{name: "pinned to one core", allowed: []int{3}, online: 32, warn: true,
			contains: []string{"CPU 3", "CPU pinning"}},
		{name: "pinned to one isolated core", allowed: []int{6}, isolated: isolated, online: 32, warn: true,
			contains: []string{"CPU 6"}},
		// A genuinely single-core machine has nothing to fix.
		{name: "single-core host", allowed: []int{0}, online: 1},
		{name: "unknown affinity", allowed: nil, isolated: isolated, online: 32},
	}
	for _, c := range cases {
		got := Evaluate(c.allowed, c.isolated, c.online)
		if (got != "") != c.warn {
			t.Errorf("%s: Evaluate = %q, want warning=%v", c.name, got, c.warn)
			continue
		}
		for _, s := range c.contains {
			if !strings.Contains(got, s) {
				t.Errorf("%s: warning %q missing %q", c.name, got, s)
			}
		}
	}
}

func TestCheckSysfs(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		isolatedPath: "6-15,22-31\n",
		onlinePath:   "0-31\n",
	}
	read := func(path string) ([]byte, error) {
		s, ok := files[path]
		if !ok {
			return nil, errors.New("missing")
		}
		return []byte(s), nil
	}
	if got := checkSysfs(read, []int{6, 7, 22, 23}); !strings.Contains(got, "isolated") {
		t.Errorf("isolated pinning: got %q, want isolation warning", got)
	}
	if got := checkSysfs(read, []int{0, 1, 16, 17}); got != "" {
		t.Errorf("non-isolated pinning: got %q, want none", got)
	}
}

func TestCheckSysfsMissingFiles(t *testing.T) {
	t.Parallel()
	missing := func(string) ([]byte, error) { return nil, errors.New("missing") }
	// No isolated list: nothing is isolated, and multi-core pinning is fine.
	if got := checkSysfs(missing, []int{6, 7}); got != "" {
		t.Errorf("got %q, want none", got)
	}
	// No online list: the host size is unknown, so single-core pinning
	// cannot be told apart from a single-core host. Stay quiet.
	if got := checkSysfs(missing, []int{3}); got != "" {
		t.Errorf("got %q, want none", got)
	}
}

func seq(lo, hi int) []int {
	out := make([]int, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}
