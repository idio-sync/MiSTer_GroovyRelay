package adapters

import "errors"

var ErrSourceUnavailable = errors.New("source unavailable")

type AUXStatus struct {
	Enabled      bool
	Configured   bool
	Active       bool
	InputID      string
	DisplayName  string
	AdapterRef   string
	ErrorMessage string
}
