package mail_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/mail/resend"
	"github.com/fastygo/market/internal/mail/unisender"
	"github.com/fastygo/market/internal/platform/tx"
)

var msg = mail.Message{To: "buyer@example.com", Subject: "Order M-2026-000001", Text: "hi", HTML: "<b>hi</b>", IdempotencyKey: "email:order:o1:paid"}

func TestFileSender(t *testing.T) {
	dir := t.TempDir()
	s := mail.FileSender{Dir: dir, From: "shop@example.com"}
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), msg); err != nil { // retry overwrites
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("files %d", len(ents))
	}
	b, _ := os.ReadFile(filepath.Join(dir, ents[0].Name()))
	if !strings.Contains(string(b), "To: buyer@example.com") || !strings.Contains(string(b), "<b>hi</b>") {
		t.Fatalf("%s", b)
	}
	bad := msg
	bad.To = "a@example.com\r\nBcc: x@example.com"
	if err := s.Send(context.Background(), bad); !errors.Is(err, mail.ErrPermanent) {
		t.Fatalf("header injection: %v", err)
	}
	if err := s.Send(tx.Mark(context.Background()), msg); !errors.Is(err, tx.ErrNetworkInTx) {
		t.Fatalf("in tx: %v", err)
	}
}

func TestUnisender(t *testing.T) {
	var got map[string]any
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/email/send.json" || r.Header.Get("X-API-KEY") != "key-1" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"status":"error","code":102,"message":"bad key"}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"success","job_id":"1"}`))
	}))
	defer srv.Close()
	s, err := unisender.New(unisender.Sender{APIKey: "key-1", BaseURL: srv.URL, From: "shop@example.com", FromName: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	m := got["message"].(map[string]any)
	if m["subject"] != msg.Subject || m["from_email"] != "shop@example.com" || len(m["idempotence_key"].(string)) != 64 ||
		m["recipients"].([]any)[0].(map[string]any)["email"] != "buyer@example.com" || m["body"].(map[string]any)["html"] != "<b>hi</b>" {
		t.Fatalf("body %v", m)
	}
	status = 503
	if err := s.Send(context.Background(), msg); err == nil || errors.Is(err, mail.ErrPermanent) {
		t.Fatalf("503 should be retryable: %v", err)
	}
	s2, _ := unisender.New(unisender.Sender{APIKey: "wrong-key-value", BaseURL: srv.URL, From: "shop@example.com"})
	if err := s2.Send(context.Background(), msg); !errors.Is(err, mail.ErrPermanent) || strings.Contains(err.Error(), "wrong-key-value") {
		t.Fatalf("401: %v", err)
	}
}

func TestResend(t *testing.T) {
	var idem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer re-1" {
			w.WriteHeader(401)
			return
		}
		idem = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{"id":"e1"}`))
	}))
	defer srv.Close()
	s, _ := resend.New(resend.Sender{APIKey: "re-1", BaseURL: srv.URL, From: "Shop <shop@example.com>"})
	if err := s.Send(context.Background(), msg); err != nil || len(idem) != 64 {
		t.Fatalf("%v %q", err, idem)
	}
}
