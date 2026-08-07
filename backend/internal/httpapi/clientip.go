package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type ipCtxKey int

const clientIPKey ipCtxKey = iota

// clientIPMiddleware records the caller's address for the audit log.
//
// X-Forwarded-For is honoured only when the deployment says it is behind a
// proxy. Trusting it unconditionally lets any caller write whatever address
// they like into the audit log, which is worse than recording the proxy's own
// address honestly.
func clientIPMiddleware(trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := directIP(r)
		if trustProxy {
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				first, _, _ := strings.Cut(fwd, ",")
				if first = strings.TrimSpace(first); first != "" {
					ip = first
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey, ip)))
	})
}

func directIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func clientIP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey).(string)
	return ip
}
