// Package yktest is an in-memory YooKassa API v3 for adapter and
// integration tests (httptest). It is not a full emulator.
package yktest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// API is a minimal in-memory YooKassa v3 for tests: idempotent create,
// get, cancel, refunds and receipts; Basic auth "shop-1":"test-secret".
type API struct {
	mu       sync.Mutex
	byKey    map[string]string
	payments map[string]map[string]any
	refunds  map[string]map[string]any
	last     map[string]any
	keys     []string
	n        int
}

// New returns an empty fake.
func New() *API {
	return &API{byKey: map[string]string{}, payments: map[string]map[string]any{}, refunds: map[string]map[string]any{}}
}

func (f *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, p, ok := r.BasicAuth()
	if !ok || u != "shop-1" || p != "test-secret" {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"type":"error","code":"invalid_credentials","description":"bad auth"}`)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	key := r.Header.Get("Idempotence-Key")
	write := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	path := strings.TrimPrefix(r.URL.Path, "/v3")
	switch {
	case r.Method == "POST" && path == "/payments":
		f.keys = append(f.keys, key)
		f.last = body
		if id, ok := f.byKey[key]; ok {
			write(f.payments[id])
			return
		}
		f.n++
		id := fmt.Sprintf("2f000000-000f-5000-8000-%012d", f.n)
		pm := map[string]any{"id": id, "status": "pending", "paid": false, "amount": body["amount"], "test": true,
			"confirmation": map[string]any{"type": "redirect", "confirmation_url": "https://yoomoney.example.com/checkout?orderId=" + id}}
		f.byKey[key], f.payments[id] = id, pm
		write(pm)
	case r.Method == "GET" && strings.HasPrefix(path, "/payments/"):
		pm, ok := f.payments[strings.TrimPrefix(path, "/payments/")]
		if !ok {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"type":"error","code":"not_found"}`)
			return
		}
		write(pm)
	case r.Method == "POST" && strings.HasSuffix(path, "/cancel"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/payments/"), "/cancel")
		f.payments[id]["status"] = "canceled"
		write(f.payments[id])
	case r.Method == "POST" && path == "/refunds":
		pm := f.payments[body["payment_id"].(string)]
		if pm == nil || pm["status"] != "succeeded" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"type":"error","code":"invalid_request","description":"not refundable"}`)
			return
		}
		pm["refunded_amount"] = body["amount"]
		rf := map[string]any{"id": "rf-1", "payment_id": body["payment_id"], "status": "succeeded"}
		f.refunds["rf-1"] = rf
		write(rf)
	case r.Method == "GET" && strings.HasPrefix(path, "/refunds/"):
		rf, ok := f.refunds[strings.TrimPrefix(path, "/refunds/")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		write(rf)
	case r.Method == "GET" && path == "/receipts":
		if r.URL.Query().Get("payment_id") != "" {
			write(map[string]any{"items": []any{map[string]any{"id": "rc-1", "type": "payment", "status": "succeeded", "fiscal_document_number": "3986"}}})
			return
		}
		write(map[string]any{"items": []any{}})
	default:
		w.WriteHeader(404)
	}
}

// Set changes a payment status (the buyer paid, the payment expired).
func (f *API) Set(id, status string) { f.mu.Lock(); f.payments[id]["status"] = status; f.mu.Unlock() }

// Last is the body of the last create request.
func (f *API) Last() map[string]any { f.mu.Lock(); defer f.mu.Unlock(); return f.last }

// Keys are the Idempotence-Key headers of create requests.
func (f *API) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.keys...)
}

// Payments is the number of distinct payments.
func (f *API) Payments() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.payments) }

// Get returns a copy of a payment object.
func (f *API) Get(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{}
	for k, v := range f.payments[id] {
		out[k] = v
	}
	return out
}
