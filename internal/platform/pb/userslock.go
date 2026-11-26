package pb

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/fastygo/market/internal/platform/clientip"
	"github.com/fastygo/market/internal/platform/ratelimit"
)

// requestBinder is the PocketBase router (or a test fake).
type requestBinder interface {
	Bind(handlers ...*hook.Handler[*core.RequestEvent]) *router.RouterGroup[*core.RequestEvent]
}

// RegisterUsersLock closes the public users API: no self-registration
// (CreateRule is nil after the migration), no PocketBase password/OTP
// login (the cabinet uses CheckPassword and magic links), and no email
// changes through the API.
func RegisterUsersLock(app core.App) {
	denyAuth := func(e *core.RequestEvent) error {
		return e.ForbiddenError("users API authentication is disabled", nil)
	}
	app.OnRecordAuthWithPasswordRequest("users").Bind(&hook.Handler[*core.RecordAuthWithPasswordRequestEvent]{
		Id:   "market_users_no_password_auth",
		Func: func(e *core.RecordAuthWithPasswordRequestEvent) error { return denyAuth(e.RequestEvent) },
	})
	app.OnRecordRequestOTPRequest("users").Bind(&hook.Handler[*core.RecordCreateOTPRequestEvent]{
		Id:   "market_users_no_otp",
		Func: func(e *core.RecordCreateOTPRequestEvent) error { return denyAuth(e.RequestEvent) },
	})
	app.OnRecordAuthWithOTPRequest("users").Bind(&hook.Handler[*core.RecordAuthWithOTPRequestEvent]{
		Id:   "market_users_no_otp_auth",
		Func: func(e *core.RecordAuthWithOTPRequestEvent) error { return denyAuth(e.RequestEvent) },
	})
	app.OnRecordRequestPasswordResetRequest("users").Bind(&hook.Handler[*core.RecordRequestPasswordResetRequestEvent]{
		Id:   "market_users_no_password_reset",
		Func: func(e *core.RecordRequestPasswordResetRequestEvent) error { return denyAuth(e.RequestEvent) },
	})
	app.OnRecordRequestVerificationRequest("users").Bind(&hook.Handler[*core.RecordRequestVerificationRequestEvent]{
		Id:   "market_users_no_verification",
		Func: func(e *core.RecordRequestVerificationRequestEvent) error { return denyAuth(e.RequestEvent) },
	})

	app.OnRecordUpdateRequest("users").Bind(&hook.Handler[*core.RecordRequestEvent]{
		Id: "market_users_no_email_change",
		Func: func(e *core.RecordRequestEvent) error {
			if e.HasSuperuserAuth() {
				return e.Next()
			}
			if e.Record != nil && e.Record.Original().Email() != e.Record.Email() {
				return e.ForbiddenError("email cannot be changed via the API", nil)
			}
			return e.Next()
		},
	})
}

// IsAuthLimitedPath is true for the PocketBase users auth/create posts
// that share the sign-in budget. Cabinet /account/login and /account/link
// are limited in the web handler so the same key is not counted twice.
func IsAuthLimitedPath(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	const prefix = "/api/collections/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(path, prefix)
	name, action, ok := strings.Cut(rest, "/")
	if !ok {
		return false
	}
	if name != "users" && name != "_pb_users_auth_" {
		return false
	}
	switch action {
	case "auth-with-password", "request-otp", "auth-with-otp",
		"request-password-reset", "request-verification", "records":
		return true
	}
	return false
}

// RegisterAuthLimit binds a per-IP limiter (trusted clientip.Resolver) on
// the sign-in routes. A 429 includes Retry-After.
func RegisterAuthLimit(r requestBinder, lim *ratelimit.Limiter, ip clientip.Resolver) {
	if r == nil || lim == nil {
		return
	}
	r.Bind(&hook.Handler[*core.RequestEvent]{
		Id: "market_auth_limit",
		Func: func(e *core.RequestEvent) error {
			if !IsAuthLimitedPath(e.Request.Method, e.Request.URL.Path) {
				return e.Next()
			}
			ok, wait := lim.Allow(ratelimit.KeyPrefix + ip.IP(e.Request))
			if ok {
				return e.Next()
			}
			ratelimit.SetRetryAfter(e.Response, wait)
			return e.TooManyRequestsError("too many requests", nil)
		},
	})
}
