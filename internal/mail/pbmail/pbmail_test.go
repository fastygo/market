package pbmail

import (
	"context"
	"errors"
	"net/textproto"
	"testing"
	"time"

	"github.com/fastygo/market/internal/mail"
	"github.com/pocketbase/pocketbase/tools/mailer"
)

type fakeClient struct {
	sent []*mailer.Message
	err  error
}

func (f *fakeClient) Send(m *mailer.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

type fakeFallback struct{ got []mail.Message }

func (*fakeFallback) Code() string { return "file" }
func (f *fakeFallback) Send(_ context.Context, m mail.Message) error {
	f.got = append(f.got, m)
	return nil
}

func msg() mail.Message {
	return mail.Message{To: "buyer@example.com", Subject: "Order M-1", Text: "hi", HTML: "<p>hi</p>", IdempotencyKey: "email:order:1:paid"}
}

func newSender(c *fakeClient, fb *fakeFallback, st Settings) *Sender {
	return &Sender{Client: func() mailer.Mailer { return c }, Settings: func() Settings { return st }, Fallback: fb}
}

func TestSendsThroughPocketBaseMailer(t *testing.T) {
	c, fb := &fakeClient{}, &fakeFallback{}
	s := newSender(c, fb, Settings{SMTPEnabled: true, SenderName: "Shop", SenderAddress: "shop@example.com"})
	if err := s.Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 1 || len(fb.got) != 0 {
		t.Fatalf("sent %d fallback %d", len(c.sent), len(fb.got))
	}
	m := c.sent[0]
	if m.From.Address != "shop@example.com" || m.From.Name != "Shop" || m.To[0].Address != "buyer@example.com" ||
		m.Subject != "Order M-1" || m.Text != "hi" || m.HTML != "<p>hi</p>" {
		t.Fatalf("message %+v", m)
	}
	id := m.Headers["Message-ID"]
	if err := s.Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	if id == "" || c.sent[1].Headers["Message-ID"] != id {
		t.Fatalf("Message-ID not stable: %q %q", id, c.sent[1].Headers["Message-ID"])
	}
}

func TestEnvSenderOverridesSettings(t *testing.T) {
	c := &fakeClient{}
	s := newSender(c, nil, Settings{SMTPEnabled: true, SenderAddress: "support@example.com"})
	s.From, s.FromName = "orders@example.com", "Market"
	if err := s.Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	if c.sent[0].From.Address != "orders@example.com" || c.sent[0].From.Name != "Market" {
		t.Fatalf("from %+v", c.sent[0].From)
	}
}

func TestFallbackWhenSMTPDisabled(t *testing.T) {
	c, fb := &fakeClient{}, &fakeFallback{}
	s := newSender(c, fb, Settings{SenderAddress: "shop@example.com"})
	if err := s.Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 0 || len(fb.got) != 1 {
		t.Fatalf("sent %d fallback %d", len(c.sent), len(fb.got))
	}
	s.Sendmail = true
	if err := s.Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	if len(c.sent) != 1 {
		t.Fatal("sendmail opt-in must use the PocketBase client")
	}
}

func TestErrorClassification(t *testing.T) {
	c := &fakeClient{err: &textproto.Error{Code: 550, Msg: "no such user"}}
	s := newSender(c, nil, Settings{SMTPEnabled: true, SenderAddress: "shop@example.com"})
	if err := s.Send(context.Background(), msg()); !errors.Is(err, mail.ErrPermanent) {
		t.Fatalf("550 must be permanent: %v", err)
	}
	for _, e := range []error{&textproto.Error{Code: 421, Msg: "busy"}, &textproto.Error{Code: 535, Msg: "auth"}, errors.New("dial tcp: timeout")} {
		c.err = e
		if err := s.Send(context.Background(), msg()); err == nil || errors.Is(err, mail.ErrPermanent) {
			t.Fatalf("%v must be retryable: %v", e, err)
		}
	}
	s2 := newSender(&fakeClient{}, nil, Settings{SMTPEnabled: true})
	if err := s2.Send(context.Background(), msg()); err == nil || errors.Is(err, mail.ErrPermanent) {
		t.Fatalf("missing sender is fixable in the admin UI and must be retryable: %v", err)
	}
	if err := s.Send(context.Background(), mail.Message{To: "bad", Subject: "x", Text: "y"}); !errors.Is(err, mail.ErrPermanent) {
		t.Fatalf("invalid message must be permanent: %v", err)
	}
}

type blockingClient struct{ release chan struct{} }

func (b blockingClient) Send(*mailer.Message) error { <-b.release; return nil }

func TestSendTimesOutOnSilentServer(t *testing.T) {
	b := blockingClient{release: make(chan struct{})}
	defer close(b.release)
	s := &Sender{Client: func() mailer.Mailer { return b }, Timeout: 20 * time.Millisecond,
		Settings: func() Settings { return Settings{SMTPEnabled: true, SenderAddress: "shop@example.com"} }}
	start := time.Now()
	err := s.Send(context.Background(), msg())
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, mail.ErrPermanent) {
		t.Fatalf("err = %v, want retryable deadline exceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("send took %v", d)
	}
}
