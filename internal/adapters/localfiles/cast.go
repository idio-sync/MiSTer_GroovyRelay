package localfiles

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/ffmpeg"
)

func (a *Adapter) Cast(ctx context.Context, libName, rel string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !a.IsEnabled() {
		return fmt.Errorf("localfiles adapter is disabled")
	}
	lib, ok := a.libraryByName(libName)
	if !ok {
		return fmt.Errorf("unknown library %q", libName)
	}
	path, err := resolveInLibrary(lib.Root, rel)
	if err != nil {
		return err
	}
	if !isPlayable(path) {
		return fmt.Errorf("file is not playable")
	}
	if a.core == nil {
		return fmt.Errorf("localfiles: core session manager is not configured")
	}

	policy := localFilePolicy()
	probeResult := a.probeBestEffort(ctx, path, policy)
	refID, err := randHex8()
	if err != nil {
		return err
	}
	title := titleFromPath(path)
	req := core.SessionRequest{
		StreamURL:        path,
		Source:           adapterName,
		AdapterRef:       adapterName + ":" + refID,
		DirectPlay:       true,
		Capabilities:     core.Capabilities{CanSeek: true, CanPause: true},
		MediaInputPolicy: policy,
		Title:            title,
		DisplayMetadata: core.DisplayMetadata{
			Primary:   title,
			Secondary: lib.Name,
		},
		MediaKind: core.MediaKindVideo,
	}
	if isAudioOnlyProbe(probeResult) {
		req.MediaKind = core.MediaKindMusic
		req.Visualizer = core.VisualizerRequest{
			Enabled: true,
			Mode:    core.VisualizerModeRetroAnalyzer,
			Metadata: core.VisualizerMetadata{
				Title:       title,
				Duration:    time.Duration(probeResult.Duration * float64(time.Second)),
				ArtworkPath: "",
			},
		}
	}
	if err := a.core.StartSession(req); err != nil {
		return err
	}
	a.recordCompanionHistory(lib.Name, rel, title, time.Now())
	return nil
}

func (a *Adapter) probeBestEffort(ctx context.Context, url string, policy ffmpeg.MediaInputPolicy) *ffmpeg.ProbeResult {
	probe := a.probe
	if probe == nil {
		probe = a.probeDefault
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	result, err := probe(probeCtx, url, policy)
	if err != nil {
		return nil
	}
	return result
}

func isAudioOnlyProbe(result *ffmpeg.ProbeResult) bool {
	return result != nil && result.AudioRate > 0 && result.Width == 0
}

func titleFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func randHex8() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
