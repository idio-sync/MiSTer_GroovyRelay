package plex

import (
	"context"
	"sync"
	"time"
)

// pendingLink tracks an in-flight plex.tv PIN flow for one adapter.
// Lives in-memory only; on bridge restart mid-flow the user starts
// over (design §10.1). The whole flow is ~30 seconds so redoing is
// cheaper than persisting.
//
// pinID matches the type RequestPIN/PollPIN use (int) — do NOT change
// to string without refactoring the linking API in lockstep.
type pendingLink struct {
	mu sync.Mutex

	code   string
	pinID  int
	expiry time.Time

	done   bool
	token  string
	errMsg string // populated on failure or expiry

	ctx    context.Context
	cancel context.CancelFunc
}

func newPendingLink(code string, pinID int, expiry time.Time) *pendingLink {
	ctx, cancel := context.WithCancel(context.Background())
	return &pendingLink{
		code:   code,
		pinID:  pinID,
		expiry: expiry,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (p *pendingLink) Code() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code
}

func (p *pendingLink) PinID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pinID
}

// TimeLeft returns the remaining time before the PIN expires. Can
// be negative.
func (p *pendingLink) TimeLeft() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Until(p.expiry)
}

func (p *pendingLink) Expired() bool {
	return p.TimeLeft() <= 0
}

func (p *pendingLink) Done() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

func (p *pendingLink) Token() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token
}

func (p *pendingLink) Error() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errMsg
}

// complete marks the flow as done with either a token (success) or
// errMsg (failure/expiry). Subsequent Done()/Token()/Error() calls
// reflect the terminal state.
func (p *pendingLink) complete(token, errMsg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	p.token = token
	p.errMsg = errMsg
}

// abandon cancels the polling goroutine's context. Safe to call
// multiple times (context.CancelFunc is idempotent).
func (p *pendingLink) abandon() {
	if p.cancel != nil {
		p.cancel()
	}
}
