package adapters

import (
	"context"
	"testing"
)

func TestLinkPhaseConstants(t *testing.T) {
	cases := map[string]string{
		LinkPhaseUnlinked: "unlinked",
		LinkPhasePending:  "pending",
		LinkPhaseLinked:   "linked",
		LinkPhaseError:    "error",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("phase constant = %q, want %q", got, want)
		}
	}
}

// Compile-time guard: a value type can satisfy LinkController.
func TestLinkControllerShape(t *testing.T) {
	var _ LinkController = (*noopLinkController)(nil)
}

type noopLinkController struct{}

func (*noopLinkController) Snapshot() LinkSnapshot { return LinkSnapshot{Phase: LinkPhaseUnlinked} }
func (*noopLinkController) StartLink(_ context.Context, _ map[string]string) (LinkSnapshot, error) {
	return LinkSnapshot{}, nil
}
func (*noopLinkController) PollLink(_ context.Context) (LinkSnapshot, error) { return LinkSnapshot{}, nil }
func (*noopLinkController) Unlink(_ context.Context) (LinkSnapshot, error)   { return LinkSnapshot{}, nil }
