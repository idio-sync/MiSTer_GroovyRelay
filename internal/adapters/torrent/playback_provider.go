package torrent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func (a *Adapter) PlaybackBanner(ctx context.Context, snap adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	if snap.Source != torrentAdapterName && !strings.HasPrefix(snap.AdapterRef, "torrent:") {
		return adapters.PlaybackBannerAdapterView{}, false
	}
	return adapters.PlaybackBannerAdapterView{
		SourceDisplay: "Torrent",
		Actions: []adapters.PlaybackAction{{
			ID:      adapters.PlaybackActionStop,
			Label:   "Stop",
			Icon:    "stop",
			Enabled: true,
		}},
	}, true
}

func (a *Adapter) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	if req.Action != adapters.PlaybackActionStop {
		return adapters.PlaybackActionResult{}, adapters.UnsupportedPlaybackActionError(fmt.Sprintf("unknown playback action %q", req.Action))
	}
	if a.core == nil {
		return adapters.PlaybackActionResult{}, fmt.Errorf("core not wired")
	}
	matched, err := a.core.StopIfSession(req.AdapterRef, req.Generation)
	if err != nil {
		return adapters.PlaybackActionResult{}, err
	}
	if !matched {
		return adapters.PlaybackActionResult{}, adapters.ErrActiveSessionChanged
	}
	return adapters.PlaybackActionResult{Message: "stopped"}, nil
}

func (a *Adapter) QuickCastTabs() []adapters.QuickCastTab {
	enabled := a.IsEnabled()
	a.mu.Lock()
	ack := a.cfg.TrafficAcknowledged
	a.mu.Unlock()
	disabled := ""
	if !enabled {
		disabled = "torrent adapter is disabled"
	} else if !ack {
		disabled = "BitTorrent traffic acknowledgement required"
	}
	return []adapters.QuickCastTab{
		{
			ID:             "torrent-magnet",
			Label:          "Magnet",
			Enabled:        enabled && ack,
			DisabledReason: disabled,
			Encoding:       adapters.QuickCastEncodingForm,
			Fields:         []adapters.QuickCastField{{Name: "magnet", Label: "Magnet", Type: "text", Required: true}},
		},
		{
			ID:             "torrent-file",
			Label:          "Torrent File",
			Enabled:        enabled && ack,
			DisabledReason: disabled,
			Encoding:       adapters.QuickCastEncodingMultipart,
			Fields:         []adapters.QuickCastField{{Name: "torrent_file", Label: "Torrent File", Type: "file", Required: true}},
		},
		{
			ID:             "torrent-url",
			Label:          "Torrent URL",
			Enabled:        enabled && ack,
			DisabledReason: disabled,
			Encoding:       adapters.QuickCastEncodingForm,
			Fields: []adapters.QuickCastField{{
				Name:        "torrent_url",
				Label:       "Torrent URL",
				Type:        "url",
				Placeholder: "https://example.com/file.torrent",
				Required:    true,
			}},
		},
	}
}

func (a *Adapter) HandleQuickCast(ctx context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	enabled := a.IsEnabled()
	a.mu.Lock()
	ack := a.cfg.TrafficAcknowledged
	a.mu.Unlock()
	if !enabled {
		return adapters.QuickCastResult{}, fmt.Errorf("torrent adapter is disabled")
	}
	if !ack {
		return adapters.QuickCastResult{}, fmt.Errorf("BitTorrent traffic acknowledgement required")
	}

	switch req.TabID {
	case "torrent-magnet":
		raw := strings.TrimSpace(req.Values["magnet"])
		if raw == "" {
			return adapters.QuickCastResult{}, fmt.Errorf("magnet is required")
		}
		started, err := a.startMagnet(ctx, raw)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	case "torrent-file":
		if req.File == nil || req.File.Header == nil {
			return adapters.QuickCastResult{}, fmt.Errorf("torrent_file is required")
		}
		f, err := req.File.Header.Open()
		if err != nil {
			return adapters.QuickCastResult{}, fmt.Errorf("open torrent_file: %w", err)
		}
		defer f.Close()
		body, err := io.ReadAll(io.LimitReader(f, maxTorrentUploadBytes+1))
		if err != nil {
			return adapters.QuickCastResult{}, fmt.Errorf("read torrent_file: %w", err)
		}
		if len(body) > maxTorrentUploadBytes {
			return adapters.QuickCastResult{}, fmt.Errorf("torrent file exceeds 4 MiB")
		}
		started, err := a.startTorrentBytes(ctx, body)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	case "torrent-url":
		raw := strings.TrimSpace(req.Values["torrent_url"])
		if raw == "" {
			return adapters.QuickCastResult{}, fmt.Errorf("torrent_url is required")
		}
		started, err := a.startTorrentURL(ctx, raw)
		if err != nil {
			return adapters.QuickCastResult{}, err
		}
		return adapters.QuickCastResult{Message: "torrent started", AdapterRef: started.AdapterRef}, nil
	default:
		return adapters.QuickCastResult{}, fmt.Errorf("unknown quick-cast tab %q", req.TabID)
	}
}
