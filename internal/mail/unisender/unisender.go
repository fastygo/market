// Package unisender is the Unisender Go (transactional) mail.Sender: HTTP
// API, RU data centre, payable from Russia. POST email/send.json with the
// X-API-KEY header; idempotence_key de-duplicates retries.
package unisender

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

// DefaultBaseURL is the documented API endpoint.
const DefaultBaseURL = "https://goapi.unisender.ru/ru/transactional/api/v1"

// Sender is the Unisender Go adapter.
type Sender struct {
	APIKey   string
	BaseURL  string
	From     string // a verified sender address on a verified domain
	FromName string
	HTTP     *http.Client
}

var _ mail.Sender = (*Sender)(nil)

// New validates the config; the key is never part of an error.
func New(s Sender) (*Sender, error) {
	if s.APIKey == "" || !strings.Contains(s.From, "@") {
		return nil, errors.New("unisender: API key and sender address are required")
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
func (s *Sender) Code() string { return "unisender" }

// Send implements mail.Sender.
func (s *Sender) Send(ctx context.Context, m mail.Message) error {
	if err := tx.AssertNoTx(ctx); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	msg := map[string]any{
		"recipients":       []map[string]any{{"email": m.To}},
		"subject":          m.Subject,
		"from_email":       s.From,
		"from_name":        s.FromName,
		"body":             map[string]string{"plaintext": m.Text, "html": m.HTML},
		"track_links":      0,
		"track_read":       0,
		"skip_unsubscribe": 1, // transactional mail; needs the account option, else ignored
	}
	if m.IdempotencyKey != "" {
		sum := sha256.Sum256([]byte(m.IdempotencyKey))
		msg["idempotence_key"] = hex.EncodeToString(sum[:])
	}
	if len(m.Tags) > 0 {
		msg["tags"] = m.Tags
	}
	b, err := json.Marshal(map[string]any{"message": msg})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/email/send.json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-KEY", s.APIKey)
	res, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("unisender: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode == http.StatusOK {
		var r struct {
			Status       string            `json:"status"`
			FailedEmails map[string]string `json:"failed_emails"`
		}
		if err := json.Unmarshal(body, &r); err == nil && len(r.FailedEmails) > 0 {
			return fmt.Errorf("%w: recipient %v", mail.ErrPermanent, r.FailedEmails)
		}
		return nil
	}
	var e struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	err = fmt.Errorf("unisender: HTTP %d code %d %s", res.StatusCode, e.Code, e.Message)
	if res.StatusCode == 400 || res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 413 {
		return fmt.Errorf("%w: %v", mail.ErrPermanent, err)
	}
	return err
}
