package torrent

import (
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type TorrentErrorKind string

const (
	ErrDisabled               TorrentErrorKind = "disabled"
	ErrTrafficNotAcknowledged TorrentErrorKind = "traffic_not_acknowledged"
	ErrBadInput               TorrentErrorKind = "bad_input"
	ErrUploadTooLarge         TorrentErrorKind = "upload_too_large"
	ErrMetadataTimeout        TorrentErrorKind = "metadata_timeout"
	ErrNoPlayableFile         TorrentErrorKind = "no_playable_file"
	ErrExpiredToken           TorrentErrorKind = "expired_token"
	ErrNonLoopback            TorrentErrorKind = "non_loopback"
	ErrCoreStart              TorrentErrorKind = "core_start"
)

type TorrentError struct {
	Kind    TorrentErrorKind
	Message string
	Err     error
}

func (e *TorrentError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func (e *TorrentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func torrentErrorStatus(err error) int {
	var terr *TorrentError
	if !errors.As(err, &terr) {
		return http.StatusInternalServerError
	}
	switch terr.Kind {
	case ErrDisabled:
		return http.StatusConflict
	case ErrTrafficNotAcknowledged, ErrNonLoopback:
		return http.StatusForbidden
	case ErrBadInput:
		return http.StatusBadRequest
	case ErrUploadTooLarge:
		return http.StatusRequestEntityTooLarge
	case ErrMetadataTimeout:
		return http.StatusGatewayTimeout
	case ErrNoPlayableFile:
		return http.StatusUnprocessableEntity
	case ErrExpiredToken:
		return http.StatusNotFound
	case ErrCoreStart:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func redactMagnet(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "magnet" {
		return "magnet:<invalid>"
	}
	for _, xt := range u.Query()["xt"] {
		const prefix = "urn:btih:"
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			continue
		}
		hash := strings.ToLower(xt[len(prefix):])
		switch len(hash) {
		case 40:
			if !isHex(hash) {
				return "magnet:<invalid>"
			}
			return "magnet:?xt=urn:btih:" + hash[:8]
		case 32:
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(hash))
			if err != nil || len(decoded) != 20 {
				return "magnet:<invalid>"
			}
			return "magnet:?xt=urn:btih:" + hex.EncodeToString(decoded[:4])
		default:
			return "magnet:<invalid>"
		}
	}
	return "magnet:<invalid>"
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}
