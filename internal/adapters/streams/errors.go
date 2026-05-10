package streams

type ErrorKind string

const (
	ErrKindNoMatch           ErrorKind = "no_match"
	ErrKindInvalidExtraction ErrorKind = "invalid_extraction"
	ErrKindProviderDisabled  ErrorKind = "provider_disabled"
)

type StreamsError struct {
	Kind    ErrorKind
	Message string
}

func (e *StreamsError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}
