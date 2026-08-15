package main

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

const deviceWebSocketPath = "/v2/device"

// deviceOriginGuard owns browser/WebView Origin policy outside coder/websocket.
// ESP32 and other non-browser device clients normally send no Origin and are
// authenticated by their enrolled Device-Id + Bearer credential. Browser-like
// callers with Origin are denied by default and require an exact HTTPS origin
// in COMPANION_ALLOWED_DEVICE_ORIGINS.
func deviceOriginGuard(next http.Handler) http.Handler {
	// Owner browser authentication is an independent HTTP surface. It is inserted
	// at the composition edge and is enabled only by explicit OIDC configuration;
	// it never changes the enrolled-device credential path below.
	next = ownerAuthFromEnvironment(next)
	allowed := allowedDeviceOrigins(os.Getenv("COMPANION_ALLOWED_DEVICE_ORIGINS"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == deviceWebSocketPath {
			origins := r.Header.Values("Origin")
			if len(origins) > 1 {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			if len(origins) == 1 {
				origin := strings.TrimSpace(origins[0])
				if _, ok := allowed[origin]; !ok {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func allowedDeviceOrigins(raw string) map[string]struct{} {
	allowed := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
			u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			continue
		}
		u.Path = ""
		allowed[u.String()] = struct{}{}
	}
	return allowed
}
