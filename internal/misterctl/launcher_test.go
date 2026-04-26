// Note: tests in this file MUST NOT call t.Parallel(). They swap the
// package-level dialAndRun variable to inject fakes; parallel tests
// would race the swap and produce flaky results. Run them serially.

package misterctl

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLaunchGroovy_PassesParams(t *testing.T) {
	var got Params
	prev := dialAndRun
	t.Cleanup(func() { dialAndRun = prev })
	dialAndRun = func(_ context.Context, p Params) error {
		got = p
		return nil
	}

	want := Params{
		Host:     "192.168.1.42",
		User:     "alice",
		Password: "hunter2",
		Timeout:  5 * time.Second,
	}
	if err := LaunchGroovy(context.Background(), want); err != nil {
		t.Fatalf("LaunchGroovy: %v", err)
	}
	if got != want {
		t.Errorf("dialAndRun got %+v, want %+v", got, want)
	}
}

func TestLaunchGroovy_PropagatesError(t *testing.T) {
	sentinel := errors.New("dial timeout")
	prev := dialAndRun
	t.Cleanup(func() { dialAndRun = prev })
	dialAndRun = func(_ context.Context, _ Params) error { return sentinel }

	err := LaunchGroovy(context.Background(), Params{Host: "x"})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel propagated", err)
	}
}

func TestLaunchGroovy_RespectsContext(t *testing.T) {
	prev := dialAndRun
	t.Cleanup(func() { dialAndRun = prev })
	dialAndRun = func(ctx context.Context, _ Params) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	err := LaunchGroovy(ctx, Params{Host: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
