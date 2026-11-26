package pb

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// PermissionsPolicy is a restrictive allow-list of empty features.
const PermissionsPolicy = "accelerometer=(), autoplay=(), camera=(), display-capture=(), encrypted-media=(), fullscreen=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), usb=(), web-share=(), interest-cohort=(), browsing-topics=()"

// SetSecurityHeaders writes the storefront baseline (and overwrites
// PocketBase's X-XSS-Protection: 1).
func SetSecurityHeaders(h http.Header) {
	h.Set("X-XSS-Protection", "0")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", PermissionsPolicy)
}

// stripCORS clears CORS headers so they are not sent on HTML pages.
func stripCORS(h http.Header) {
	h.Del("Access-Control-Allow-Origin")
	h.Del("Access-Control-Allow-Credentials")
	h.Del("Access-Control-Allow-Headers")
	h.Del("Access-Control-Allow-Methods")
	h.Del("Access-Control-Expose-Headers")
	h.Del("Access-Control-Max-Age")
}

type corsFilter struct {
	http.ResponseWriter
	api bool
}

func (w *corsFilter) WriteHeader(code int) {
	if !w.api {
		stripCORS(w.Header())
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *corsFilter) Write(b []byte) (int, error) {
	if !w.api {
		stripCORS(w.Header())
	}
	return w.ResponseWriter.Write(b)
}

// RegisterSecurityHeaders adds Permissions-Policy and CORP, sets
// X-XSS-Protection to 0, and removes Access-Control-Allow-Origin from
// non-API responses (PocketBase otherwise defaults CORS to * on HTML).
func RegisterSecurityHeaders(r requestBinder) {
	if r == nil {
		return
	}
	r.Bind(&hook.Handler[*core.RequestEvent]{
		Id: "market_security_headers",
		Func: func(e *core.RequestEvent) error {
			SetSecurityHeaders(e.Response.Header())
			path := ""
			if e.Request != nil && e.Request.URL != nil {
				path = e.Request.URL.Path
			}
			e.Response = &corsFilter{ResponseWriter: e.Response, api: strings.HasPrefix(path, "/api/")}
			return e.Next()
		},
	})
}
