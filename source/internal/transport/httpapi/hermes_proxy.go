package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"market-share-go/internal/notice"
	"net/http"
)

type hermesProxyContextKey struct{}

// WithHermesProxyAuthorization admits the managed router's private transport
// only for the Hermes endpoint. The handler still validates the independent
// ingest bearer token and the normal request schema. Standalone mode never
// installs this middleware and retains its loopback-only boundary.
func WithHermesProxyAuthorization(next http.Handler, controlToken string) http.Handler {
	if len(controlToken) < 32 {
		return next
	}
	want := sha256.Sum256([]byte(controlToken))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if notice.HermesInternalRoute(request.URL.Path) {
			got := sha256.Sum256([]byte(request.Header.Get("X-Marketgo-Hermes-Proxy")))
			if subtle.ConstantTimeCompare(want[:], got[:]) == 1 {
				request = request.WithContext(context.WithValue(request.Context(), hermesProxyContextKey{}, true))
			}
		}
		next.ServeHTTP(response, request)
	})
}

func hermesTransportAllowed(request *http.Request) bool {
	trusted, _ := request.Context().Value(hermesProxyContextKey{}).(bool)
	return isLoopbackRequest(request.RemoteAddr) || trusted
}
