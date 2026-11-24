// Package pbmail sends transactional mail through the PocketBase mailer
// (ADR-0026 amendment): the SMTP settings an operator enters in the admin
// UI (Settings, Mail settings) are used as is, the same transport that
// PocketBase uses for its own verification, password reset and OTP mail.
//
// Settings are read on every send, so enabling SMTP in the admin UI takes
// effect without a restart. While SMTP is disabled the message goes to the
// Fallback sender (the development file sender) unless Sendmail is set,
// in which case PocketBase's sendmail client is used, as PocketBase does.
package pbmail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"net/textproto"
	"sync"

	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/platform/tx"
	"github.com/pocketbase/pocketbase/tools/mailer"
)

// Settings is the subset of the PocketBase settings the sender needs.
type Settings struct {
	SMTPEnabled   bool
	SenderName    string
	SenderAddress string
}

// Sender implements mail.Sender over the PocketBase mailer.
type Sender struct {
	// Client returns the PocketBase mail client (app.NewMailClient).
	Client func() mailer.Mailer
	// Settings returns the current PocketBase mail settings.
	Settings func() Settings
	// From and FromName override the PocketBase sender (MARKET_MAIL_FROM,
	// MARKET_MAIL_FROM_NAME).
	From, FromName string
	// Sendmail uses the PocketBase sendmail client while SMTP is disabled
	// instead of Fallback.
	Sendmail bool
	// Fallback receives messages while SMTP is disabled (file sender).
	Fallback mail.Sender
	Log      *slog.Logger

	warn sync.Once
}

// Code implements mail.Sender.
func (*Sender) Code() string { return "pocketbase" }

// Send implements mail.Sender. SMTP has no idempotency key, so a stable
// Message-ID derived from the outbox key lets mailbox providers collapse
// a duplicate if a retry happens after a lost acknowledgement.
func (s *Sender) Send(ctx context.Context, m mail.Message) error {
	if err := tx.AssertNoTx(ctx); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	st := s.Settings()
	if !st.SMTPEnabled && !s.Sendmail {
		s.warn.Do(func() {
			if s.Log != nil {
				s.Log.Warn("MARKET_MAIL_PROVIDER=pocketbase but SMTP is disabled in Settings > Mail settings: mail goes to the fallback sender",
					"fallback", fallbackCode(s.Fallback))
			}
		})
		if s.Fallback == nil {
			return errors.New("pbmail: SMTP disabled and no fallback sender")
		}
		return s.Fallback.Send(ctx, m)
	}
	from := netmail.Address{Name: st.SenderName, Address: st.SenderAddress}
	if s.From != "" {
		from = netmail.Address{Name: s.FromName, Address: s.From}
	}
	if from.Address == "" {
		return fmt.Errorf("%w: no sender address (MARKET_MAIL_FROM or Settings > Application sender)", mail.ErrPermanent)
	}
	msg := &mailer.Message{
		From:    from,
		To:      []netmail.Address{{Address: m.To}},
		Subject: m.Subject,
		HTML:    m.HTML,
		Text:    m.Text,
	}
	if m.IdempotencyKey != "" {
		sum := sha256.Sum256([]byte(m.IdempotencyKey))
		msg.Headers = map[string]string{"Message-ID": "<" + hex.EncodeToString(sum[:16]) + "@" + domain(from.Address) + ">"}
	}
	if err := s.Client().Send(msg); err != nil {
		return classify(err)
	}
	return nil
}

// classify marks SMTP answers a retry cannot fix (mailbox unavailable,
// rejected recipient) as permanent; connection errors, 4xx and
// authentication failures (fixable in the admin UI) stay retryable.
func classify(err error) error {
	var te *textproto.Error
	if errors.As(err, &te) && te.Code >= 550 && te.Code <= 554 {
		return fmt.Errorf("%w: %v", mail.ErrPermanent, err)
	}
	return err
}

func domain(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == '@' {
			return addr[i+1:]
		}
	}
	return "localhost"
}

func fallbackCode(s mail.Sender) string {
	if s == nil {
		return "none"
	}
	return s.Code()
}
