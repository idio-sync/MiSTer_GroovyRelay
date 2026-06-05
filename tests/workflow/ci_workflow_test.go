package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerLatestTagTracksMainBranch(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}

	workflow := string(data)
	want := "type=raw,value=latest,enable=${{ github.ref == 'refs/heads/main' }}"
	if !strings.Contains(workflow, want) {
		t.Fatalf("latest tag rule missing:\nwant workflow to contain %q", want)
	}

	legacyReleaseRule := "type=raw,value=latest,enable=${{ startsWith(github.ref, 'refs/tags/v') }}"
	if strings.Contains(workflow, legacyReleaseRule) {
		t.Fatalf("latest still tracks release tags via %q", legacyReleaseRule)
	}
}
