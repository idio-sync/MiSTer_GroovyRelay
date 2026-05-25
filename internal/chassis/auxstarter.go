package chassis

import (
	"context"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type AUXStarter interface {
	AUXStatus(context.Context) adapters.AUXStatus
	StartAUX(context.Context, string) (adapterRef string, err error)
	StopAUX(context.Context, string) (matched bool, err error)
}
