package pb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/platform/clientip"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
	"github.com/fastygo/market/internal/platform/ratelimit"
)

func usersAPI(t *testing.T, app core.App) http.Handler {
	t.Helper()
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	pb.RegisterAuthLimit(r, ratelimit.NewAuth(), clientip.Resolver{})
	pb.RegisterSecurityHeaders(r)
	h, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "192.0.2.10:1234"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUsersCreateAndPasswordAuthDenied(t *testing.T) {
	app := pbtest.New(t)
	h := usersAPI(t, app)
	rec := doJSON(t, h, http.MethodPost, "/api/collections/users/records", map[string]any{
		"email": "attacker@example.com", "password": "AttackerPass1!", "passwordConfirm": "AttackerPass1!",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/api/collections/users/auth-with-password", map[string]any{
		"identity": "buyer@example.com", "password": "x",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("auth-with-password: %d %s", rec.Code, rec.Body.String())
	}
}

func TestEnsureDoesNotAttachToAPICreatedAccount(t *testing.T) {
	app := pbtest.New(t)
	h := usersAPI(t, app)
	email := "victim@example.com"
	attackerPass := "AttackerPass1!"
	if rec := doJSON(t, h, http.MethodPost, "/api/collections/users/records", map[string]any{
		"email": email, "password": attackerPass, "passwordConfirm": attackerPass,
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("pre-register: %d %s", rec.Code, rec.Body.String())
	}
	users := &pb.Users{App: app}
	u, created, err := users.Ensure(context.Background(), email)
	if err != nil || !created || u.Email != email {
		t.Fatalf("Ensure: user=%+v created=%v err=%v", u, created, err)
	}
	if _, err := users.CheckPassword(context.Background(), email, attackerPass); err == nil {
		t.Fatal("guest account accepted the attacker password; hijack path still open")
	}
}

func TestUsersEmailChangeDeniedViaAPI(t *testing.T) {
	app := pbtest.New(t)
	col, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	rec := core.NewRecord(col)
	rec.SetEmail("buyer@example.com")
	rec.SetPassword("test-password-123")
	if err := app.Save(rec); err != nil {
		t.Fatal(err)
	}
	tok, err := rec.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"email": "other@example.com"})
	req := httptest.NewRequest(http.MethodPatch, "/api/collections/users/records/"+rec.Id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.RemoteAddr = "192.0.2.11:1"
	w := httptest.NewRecorder()
	usersAPI(t, app).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("email change: %d %s", w.Code, w.Body.String())
	}
	fresh, err := app.FindRecordById("users", rec.Id)
	if err != nil || fresh.Email() != "buyer@example.com" {
		t.Fatalf("email was changed: %v %v", err, fresh)
	}
}

func TestUsersAuthRateLimit(t *testing.T) {
	app := pbtest.New(t)
	h := usersAPI(t, app)
	var last *httptest.ResponseRecorder
	for i := 0; i < ratelimit.DefaultAuthMax+1; i++ {
		last = doJSON(t, h, http.MethodPost, "/api/collections/users/auth-with-password", map[string]any{
			"identity": "a@example.com", "password": "x",
		})
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d %s", last.Code, last.Body.String())
	}
	if last.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestSecurityHeadersAndNoCORSOnHTML(t *testing.T) {
	app := pbtest.New(t)
	h := usersAPI(t, app)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("ACAO on non-API: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("X-XSS-Protection") != "0" {
		t.Fatalf("X-XSS-Protection %q", rec.Header().Get("X-XSS-Protection"))
	}
	if rec.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("CORP %q", rec.Header().Get("Cross-Origin-Resource-Policy"))
	}
	if !strings.Contains(rec.Header().Get("Permissions-Policy"), "camera=()") {
		t.Fatalf("Permissions-Policy %q", rec.Header().Get("Permissions-Policy"))
	}
}
