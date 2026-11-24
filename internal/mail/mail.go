// Package mail is the transactional mail port (ADR-0026): one Sender per
// provider package (unisender, resend) plus the development FileSender.
// Senders are called only from the outbox worker or a request handler,
// never inside a write transaction; the outbox key is passed on as the
// provider idempotency key so a retry does not send twice.
package mail

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fastygo/market/internal/platform/tx"
)

// Message is one transactional email.
type Message struct {
	To             string
	Subject        string
	Text           string
	HTML           string
	IdempotencyKey string // stable per logical email (outbox key)
	Tags           []string
}

// Sender delivers a message.
type Sender interface {
	Code() string
	Send(ctx context.Context, m Message) error
}

// ErrPermanent wraps provider answers a retry cannot fix (bad key,
// rejected recipient); other errors are retried by the outbox.
var ErrPermanent = errors.New("mail: provider rejected the message")

// Validate checks the fields every provider needs.
func (m Message) Validate() error {
	if !strings.Contains(m.To, "@") || strings.ContainsAny(m.To, "\r\n,;") {
		return fmt.Errorf("%w: bad recipient", ErrPermanent)
	}
	if m.Subject == "" || (m.Text == "" && m.HTML == "") {
		return fmt.Errorf("%w: empty subject or body", ErrPermanent)
	}
	return nil
}

// FileSender writes each message as an .eml file (development and
// previews without a mail provider). Nothing is delivered.
type FileSender struct {
	Dir  string
	From string
	Now  func() time.Time
}

// Code implements Sender.
func (FileSender) Code() string { return "file" }

// Send implements Sender; the file name is derived from the idempotency
// key, so a retry overwrites the same file.
func (f FileSender) Send(ctx context.Context, m Message) error {
	if err := tx.AssertNoTx(ctx); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	var b strings.Builder
	w := multipart.NewWriter(&b)
	from := f.From
	if from == "" {
		from = "noreply@example.com"
	}
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%s\r\n\r\n",
		from, m.To, mime.QEncoding.Encode("utf-8", m.Subject), now().UTC().Format(time.RFC1123Z), w.Boundary())
	for _, part := range []struct{ ct, body string }{{"text/plain; charset=utf-8", m.Text}, {"text/html; charset=utf-8", m.HTML}} {
		if part.body == "" {
			continue
		}
		pw, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {part.ct}})
		if err != nil {
			return err
		}
		if _, err := pw.Write([]byte(part.body)); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	name := safeName(m.IdempotencyKey)
	if name == "" {
		name = now().UTC().Format("20060102T150405.000000000")
	}
	return os.WriteFile(filepath.Join(f.Dir, name+".eml"), []byte(b.String()), 0o600)
}

func safeName(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), ".")
}
