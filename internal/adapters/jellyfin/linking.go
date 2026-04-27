package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// jfHTTPClient is the shared HTTP client for JF REST calls. 10 s
// timeout bounds every network call; the bridge must never wait on a
// hung remote under a caller that holds a mutex or drives a ticker.
// Declared as a var so tests can swap a faster client.
var jfHTTPClient = &http.Client{Timeout: 10 * time.Second}

// AuthHeaderInput carries the components of the MediaBrowser
// Authorization header. Token is optional — omit it on the
// AuthenticateByName call itself.
type AuthHeaderInput struct {
	Token    string
	Client   string
	Device   string
	DeviceID string
	Version  string
}

// sanitizeAuthValue strips characters that would break a literal-
// quoted MediaBrowser header field: embedded double quotes, backslashes,
// and any ASCII control characters (CR/LF/NUL etc.). JF parses this
// header with a simple scanner that does not honour Go's `%q` escape
// syntax (\" / \\), so we cannot rely on fmt.Sprintf("%q", ...) for
// arbitrary input. In practice every value we feed in is either
// alphanumeric (tokens, UUIDs) or a hardcoded constant; this is a
// belt-and-braces guard against future mistakes.
func sanitizeAuthValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '"' || r == '\\' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// BuildAuthHeader constructs the value of the Authorization header
// for a JF REST call. JF accepts the format
//
//	MediaBrowser Token="...", Client="...", Device="...", DeviceId="...", Version="..."
//
// (Or X-Emby-Authorization with the same value, or X-Emby-Token /
// X-MediaBrowser-Token / ?api_key=. We use the canonical form for
// authenticated REST and ?api_key= for the WebSocket handshake.)
//
// Inputs are sanitized via sanitizeAuthValue so a token containing a
// literal double-quote or backslash cannot break header parsing — a
// guardrail against future bugs (real JF tokens are alphanumeric).
func BuildAuthHeader(in AuthHeaderInput) string {
	parts := []string{}
	if in.Token != "" {
		parts = append(parts, `Token="`+sanitizeAuthValue(in.Token)+`"`)
	}
	if in.Client != "" {
		parts = append(parts, `Client="`+sanitizeAuthValue(in.Client)+`"`)
	}
	if in.Device != "" {
		parts = append(parts, `Device="`+sanitizeAuthValue(in.Device)+`"`)
	}
	if in.DeviceID != "" {
		parts = append(parts, `DeviceId="`+sanitizeAuthValue(in.DeviceID)+`"`)
	}
	if in.Version != "" {
		parts = append(parts, `Version="`+sanitizeAuthValue(in.Version)+`"`)
	}
	return "MediaBrowser " + strings.Join(parts, ", ")
}

// AuthRequest carries the inputs to AuthenticateByName.
type AuthRequest struct {
	ServerURL string
	Username  string
	Password  string
	DeviceID  string
	Version   string
}

// AuthResult is the post-link state we persist via tokenstore.
type AuthResult struct {
	AccessToken string
	UserID      string
	UserName    string
	ServerID    string
}

// authResponseDTO mirrors the JF AuthenticationResult schema. Field
// names match the JSON the server returns.
type authResponseDTO struct {
	AccessToken string `json:"AccessToken"`
	User        struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	} `json:"User"`
	ServerID string `json:"ServerId"`
}

// AuthenticateByName POSTs to /Users/AuthenticateByName. On 200,
// returns the decoded AuthResult. On 401, returns an error containing
// "invalid credentials". On any other failure, returns an error
// containing "server unreachable" or the status code.
func AuthenticateByName(ctx context.Context, req AuthRequest) (AuthResult, error) {
	body, err := json.Marshal(map[string]string{
		"Username": req.Username,
		"Pw":       req.Password,
	})
	if err != nil {
		return AuthResult{}, fmt.Errorf("jellyfin: marshal auth body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(req.ServerURL, "/")+"/Users/AuthenticateByName",
		bytes.NewReader(body))
	if err != nil {
		return AuthResult{}, fmt.Errorf("jellyfin: build auth request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", BuildAuthHeader(AuthHeaderInput{
		// Note: NO Token here. AuthenticateByName is exactly the call
		// that obtains the token; including a stale token would be
		// rejected as malformed by some JF versions.
		Client:   "MiSTer_GroovyRelay",
		Device:   "MiSTer",
		DeviceID: req.DeviceID,
		Version:  req.Version,
	}))

	resp, err := jfHTTPClient.Do(httpReq)
	if err != nil {
		return AuthResult{}, fmt.Errorf("jellyfin: server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return AuthResult{}, errors.New("jellyfin: invalid credentials")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AuthResult{}, fmt.Errorf("jellyfin: server unreachable: HTTP %d", resp.StatusCode)
	}

	var dto authResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return AuthResult{}, fmt.Errorf("jellyfin: decode auth response: %w", err)
	}
	if dto.AccessToken == "" {
		return AuthResult{}, errors.New("jellyfin: server returned empty AccessToken")
	}
	return AuthResult{
		AccessToken: dto.AccessToken,
		UserID:      dto.User.ID,
		UserName:    dto.User.Name,
		ServerID:    dto.ServerID,
	}, nil
}

// probeSystemInfo issues a GET /System/Info?api_key=<token>. Returns
// nil on 2xx; errAuthRejected on 401; a wrapped HTTP error on other
// failures.
func probeSystemInfo(ctx context.Context, serverURL, token string) error {
	q := url.Values{}
	q.Set("api_key", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(serverURL, "/")+"/System/Info?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := jfHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin: probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errAuthRejected
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("jellyfin: probe: HTTP %d", resp.StatusCode)
	}
	return nil
}

var errAuthRejected = errors.New("jellyfin: token rejected (401)")

func isAuthError(err error) bool {
	return errors.Is(err, errAuthRejected)
}
