package streams

import (
	"context"
	"net"
	"net/http"
	"net/netip"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/sourcefetch"
)

const maxFetchRedirects = 3

type hostResolver = sourcefetch.Resolver
type fetchLimits = sourcefetch.Limits
type fetchCondition = sourcefetch.Condition
type fetchResponse = sourcefetch.Response
type validatedFetchTarget = sourcefetch.Target

type secureFetcher struct {
	resolver    hostResolver
	transport   *http.Transport
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func (f secureFetcher) Fetch(ctx context.Context, rawURL string, limits fetchLimits) ([]byte, error) {
	resp, err := f.FetchConditional(ctx, rawURL, limits, fetchCondition{})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (f secureFetcher) FetchConditional(ctx context.Context, rawURL string, limits fetchLimits, condition fetchCondition) (fetchResponse, error) {
	limits.MaxRedirects = maxFetchRedirects
	if len(limits.AllowedSchemes) == 0 {
		if limits.AllowLocalURLs {
			limits.AllowedSchemes = []string{"http", "https"}
		} else {
			limits.AllowedSchemes = []string{"https"}
		}
	}
	return sourcefetch.Fetcher{
		Resolver:    f.resolver,
		Transport:   f.transport,
		DialContext: f.dialContext,
	}.Fetch(ctx, http.MethodGet, rawURL, limits, condition)
}

func isPublicIP(addr netip.Addr) bool {
	return sourcefetch.IsPublicRoutable(addr)
}

func resolveValidatedIP(ctx context.Context, resolver hostResolver, hostname string, allowLocal bool) (netip.Addr, error) {
	return sourcefetch.ResolvePublicTargetIP(ctx, resolver, hostname, allowLocal)
}
