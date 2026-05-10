package streams

import "time"

type RefreshStatus struct {
	ProviderID string
	Source     string
	FetchedAt  time.Time
	Err        error
}
