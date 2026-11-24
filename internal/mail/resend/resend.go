// Package resend is the Resend mail.Sender (HTTP API, Idempotency-Key
// header). An alternative for non-RU deployments; see ADR-0026 for why it
// is not the RU default.
package resend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/platform/tx"
)

// DefaultBaseURL is the API endpoint.
const DefaultBaseURL = "https://api.resend.com"

// Sender is the Resend adapter.
type Sender struct {
	APIKey  string
	BaseURL string
	From    string // "Name <address>" on a verified domain
	HTTP    *http.Client
}

var _ mail.Sender = (*Sender)(nil)

// New validates the config; the key is never part of an error.
func New(s Sender) (*Sender, error) {
	if s.APIKey == "" || !strings.Contains(s.From, "@") {
		return nil, errors.New("resend: API key and sender address are required")
	}
	if s.BaseURL == "" {
		s.BaseURL = DefaultBaseURL
	}
	s.BaseURL = strings.TrimRight(s.BaseURL, "/")
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	return &s, nil
}

// Code implements mail.Sender.
func (s *Sender) Code() string { return "resend" }

// Send implements mail.Sender.
func (s *Sender) Send(ctx context.Context, m mail.Message) error {
	if err := tx.AssertNoTx(ctx); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(map[string]any{"from": s.From, "to": []string{m.To}, "subject": m.Subject, "text": m.Text, "html": m.HTML})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/emails", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	if m.IdempotencyKey != "" {
		sum := sha256.Sum256([]byte(m.IdempotencyKey))
		req.Header.Set("Idempotency-Key", hex.EncodeToString(sum[:]))
	}
	res, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	var e struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	err = fmt.Errorf("resend: HTTP %d %s %s", res.StatusCode, e.Name, e.Message)
	if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != 429 {
		return fmt.Errorf("%w: %v", mail.ErrPermanent, err)
	}
	return err
}
