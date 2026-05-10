package streams

import (
	"context"
	"time"
)

type RefreshStatus struct {
	ProviderID string
	Source     string
	FetchedAt  time.Time
	Err        error
}

func (a *Adapter) RefreshNow(ctx context.Context, providerID string) RefreshStatus {
	status := RefreshStatus{ProviderID: providerID}
	if a.refreshOnce != nil {
		status = a.refreshOnce(ctx, "manual")
		if status.ProviderID == "" {
			status.ProviderID = providerID
		}
	}
	return status
}
