package streams

import (
	"context"
	"sync"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/url/ytdlp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func newTestAdapterWithCatalog(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mtvDef := bundledMTVDefinition()
	cartoonDef := bundledCartoonDefinition()
	mtvCat := ProviderCatalog{
		ProviderID: "mtv-rewind",
		Name:       "MTV Rewind",
		Channels: []Channel{{
			ID:       "metal",
			Name:     "Metal",
			PlayMode: PlayShuffle,
			Items: []StreamItem{{
				ID:       "dQw4w9WgXcQ",
				SourceID: "dQw4w9WgXcQ",
				URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			}},
		}},
	}
	cartoonCat := ProviderCatalog{
		ProviderID: "cartoon-rewind",
		Name:       "Cartoon Rewind",
		Channels: []Channel{{
			ID:       "heman",
			Name:     "He-Man",
			PlayMode: PlayShuffle,
			Items: []StreamItem{{
				ID:       "9bZkp7q19f0",
				SourceID: "9bZkp7q19f0",
				URL:      "https://www.youtube.com/watch?v=9bZkp7q19f0",
			}},
		}},
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{mtvDef, cartoonDef})
	a.replaceCatalogsForTest([]ProviderCatalog{mtvCat, cartoonCat})
	a.core = &fakeCore{}
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}
	return a
}

type fakeCore struct {
	mu            sync.Mutex
	lastReq       core.SessionRequest
	startErr      error
	startErrs     []error
	startCalls    int
	stopCalls     int
	pauseCalls    int
	rawStopCalls  int
	rawPauseCalls int
	status        core.SessionStatus
	startHook     func(core.SessionRequest)
	stopHook      func()
	pauseIfHook   func(string)
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	f.lastReq = req
	f.startCalls++
	startHook := f.startHook
	f.mu.Unlock()

	if startHook != nil {
		startHook(req)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.startErrs) > 0 {
		err := f.startErrs[0]
		f.startErrs = f.startErrs[1:]
		if err == nil {
			f.status.AdapterRef = req.AdapterRef
		}
		return err
	}
	if f.startErr == nil {
		f.status.AdapterRef = req.AdapterRef
	}
	return f.startErr
}

func (f *fakeCore) Stop() error {
	f.mu.Lock()
	f.rawStopCalls++
	f.status.AdapterRef = ""
	stopHook := f.stopHook
	f.mu.Unlock()

	if stopHook != nil {
		stopHook()
	}
	return nil
}

func (f *fakeCore) StopIfAdapterRef(ref string) (bool, error) {
	f.mu.Lock()
	if ref == "" || f.status.AdapterRef != ref {
		f.mu.Unlock()
		return false, nil
	}
	f.stopCalls++
	f.status.AdapterRef = ""
	stopHook := f.stopHook
	f.mu.Unlock()

	if stopHook != nil {
		stopHook()
	}
	return true, nil
}

func (f *fakeCore) Pause() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rawPauseCalls++
	return nil
}

func (f *fakeCore) PauseIfAdapterRef(ref string) (bool, error) {
	if f.pauseIfHook != nil {
		f.pauseIfHook(ref)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref == "" || f.status.AdapterRef != ref {
		return false, nil
	}
	f.pauseCalls++
	return true, nil
}

func (f *fakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

type fakeResolver struct {
	res       *ytdlp.Resolution
	err       error
	responses []fakeResolveResponse
	calls     int
	pageURLs  []string
	format    string
}

type fakeResolveResponse struct {
	res *ytdlp.Resolution
	err error
}

func (f *fakeResolver) Resolve(ctx context.Context, pageURL, format, cookiesPath string) (*ytdlp.Resolution, error) {
	f.calls++
	f.pageURLs = append(f.pageURLs, pageURL)
	f.format = format
	if len(f.responses) > 0 {
		next := f.responses[0]
		f.responses = f.responses[1:]
		return next.res, next.err
	}
	return f.res, f.err
}

func newTestAdapterWithFakeCore(t *testing.T) (*Adapter, *fakeCore) {
	t.Helper()
	c := &fakeCore{}
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(true)
	a.core = c
	a.resolver = &fakeResolver{res: &ytdlp.Resolution{URL: "https://media.example/video.mp4"}}
	return a, c
}

func (a *Adapter) replaceDefinitionsForTest(defs []ProviderDefinition) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.definitions = map[string]ProviderDefinition{}
	a.definitionOrder = a.definitionOrder[:0]
	for _, def := range defs {
		a.definitions[def.ID] = def
		a.definitionOrder = append(a.definitionOrder, def.ID)
	}
}

func (a *Adapter) replaceCatalogsForTest(cats []ProviderCatalog) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.catalogs = map[string]ProviderCatalog{}
	for _, cat := range cats {
		a.catalogs[cat.ProviderID] = cat
	}
}
