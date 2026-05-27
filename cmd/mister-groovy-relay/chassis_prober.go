package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/chassis"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// underlyingProber matches the shape of the existing bridgeMisterProber.Probe
// in launcher.go. Defined as an interface here (rather than the concrete
// type) so chassis_prober_test.go can substitute a fake without spinning up
// sockets.
type underlyingProber interface {
	Probe(ctx context.Context) error
}

// chassisProber adapts the existing bridgeMisterProber to the chassis-side
// chassis.Prober interface. The chassis package does not import groovynet
// or any cmd-package types — this wrapper is the bridge.
//
// The underlying prober reads bridge.mister.{host,port,source_port=0}
// internally from its captured ui.BridgeSaver and returns only an error.
// This wrapper measures latency itself and normalizes net.Error timeouts
// to context.DeadlineExceeded so the chassis handler's timeout branch
// (errors.Is(err, context.DeadlineExceeded)) fires.
type chassisProber struct {
	inner underlyingProber
}

func newChassisProber(inner underlyingProber) *chassisProber {
	return &chassisProber{inner: inner}
}

// ProbeMister implements chassis.Prober.
//
// The bridge argument supplies Host/Port for the response only — the
// underlying prober uses its captured ui.BridgeSaver.Current(). In
// production the chassis BridgeSettingsSaver and the underlying
// prober's ui.BridgeSaver are the same instance, so the values match.
func (p *chassisProber) ProbeMister(ctx context.Context, bridge config.BridgeConfig) (chassis.ProbeResult, error) {
	start := time.Now()
	err := p.inner.Probe(ctx)
	elapsed := time.Since(start)
	if err != nil {
		return chassis.ProbeResult{}, normalizeProbeError(err)
	}
	return chassis.ProbeResult{
		LatencyMs: float64(elapsed) / float64(time.Millisecond),
		Host:      bridge.MiSTer.Host,
		Port:      bridge.MiSTer.Port,
	}, nil
}

// normalizeProbeError unwraps net.Error timeouts (which bridgeMisterProber
// wraps as `fmt.Errorf("status ack timeout after %s: %w", timeout, netErr)`)
// to context.DeadlineExceeded so the chassis handler's timeout branch can
// detect them with errors.Is. Other errors pass through unchanged.
func normalizeProbeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		// Preserve the original chain so logs still get the underlying
		// detail; the chassis handler only needs Is() to match.
		return fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}
	return err
}

// Compile-time conformance check.
var _ chassis.Prober = (*chassisProber)(nil)
