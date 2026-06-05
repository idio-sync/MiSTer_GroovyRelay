package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// fakeUnderlyingProber matches the shape of the real bridgeMisterProber.Probe
// (single ctx arg returning error). The chassis_prober wrapper measures
// latency at its own level since the inner prober doesn't report it.
type fakeUnderlyingProber struct {
	sleep time.Duration
	err   error
}

func (f *fakeUnderlyingProber) Probe(ctx context.Context) error {
	if f.sleep > 0 {
		time.Sleep(f.sleep)
	}
	return f.err
}

func TestChassisProber_SuccessReportsMeasuredLatency(t *testing.T) {
	t.Parallel()
	cp := &chassisProber{inner: &fakeUnderlyingProber{sleep: 10 * time.Millisecond}}
	res, err := cp.ProbeMister(context.Background(), config.BridgeConfig{
		MiSTer: config.MisterConfig{Host: "1.2.3.4", Port: 32100},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.LatencyMs < 5.0 {
		t.Errorf("LatencyMs = %f, want >= 5 (slept 10ms)", res.LatencyMs)
	}
	if res.Host != "1.2.3.4" || res.Port != 32100 {
		t.Errorf("res = %+v, want host/port from arg", res)
	}
}

func TestChassisProber_ContextDeadlineExceededPassesThrough(t *testing.T) {
	t.Parallel()
	cp := &chassisProber{inner: &fakeUnderlyingProber{err: context.DeadlineExceeded}}
	_, err := cp.ProbeMister(context.Background(), config.BridgeConfig{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

// fakeNetTimeoutError mimics net.Error with Timeout()=true, which is what
// the underlying bridgeMisterProber returns wrapped via fmt.Errorf.
type fakeNetTimeoutError struct{}

func (fakeNetTimeoutError) Error() string   { return "i/o timeout" }
func (fakeNetTimeoutError) Timeout() bool   { return true }
func (fakeNetTimeoutError) Temporary() bool { return false }

func TestChassisProber_NetErrorTimeoutIsNormalizedToContextDeadlineExceeded(t *testing.T) {
	t.Parallel()
	// Reproduce launcher.go:84-86's exact wrapping shape: a single
	// fmt.Errorf("...: %w", netErr) where netErr satisfies net.Error
	// with Timeout()=true. Using errors.Join here would not catch a
	// regression where the production wrapping stops including the
	// inner net.Error in the chain.
	wrapped := fmt.Errorf("status ack timeout after 1s: %w", fakeNetTimeoutError{})
	cp := &chassisProber{inner: &fakeUnderlyingProber{err: wrapped}}
	_, err := cp.ProbeMister(context.Background(), config.BridgeConfig{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded (normalized from net.Error timeout)", err)
	}
}

func TestChassisProber_SocketErrorPassesThroughUnchanged(t *testing.T) {
	t.Parallel()
	cp := &chassisProber{inner: &fakeUnderlyingProber{err: errors.New("open UDP probe socket: bind: permission denied")}}
	_, err := cp.ProbeMister(context.Background(), config.BridgeConfig{})
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("socket error should NOT normalize to context.DeadlineExceeded")
	}
}

func TestChassisProber_SatisfiesChassisInterface(t *testing.T) {
	t.Parallel()
	var _ chassis.Prober = (*chassisProber)(nil)
}

// Compile-time check: chassisProber.inner accepts the real bridgeMisterProber
// (which has Probe(ctx) error). If the underlying signature ever changes,
// this fails to build first.
func TestChassisProber_AcceptsBridgeMisterProber(t *testing.T) {
	t.Parallel()
	var _ underlyingProber = bridgeMisterProber{}
}

// Silence import "net" usage in the rare case the editor strips it.
var _ net.Error = fakeNetTimeoutError{}
