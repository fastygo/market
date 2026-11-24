package fakeissuer

import (
	"context"
	"errors"
	"testing"

	"github.com/fastygo/market/internal/platform/tx"
	"github.com/fastygo/market/internal/taxdocs"
)

func TestFakeIssuer(t *testing.T) {
	i := New()
	ctx := context.Background()
	d := taxdocs.Document{IdempotencyKey: "taxdoc:1", Operation: "income", Total: 100}
	if _, err := i.Issue(tx.Mark(ctx), d); !errors.Is(err, tx.ErrNetworkInTx) {
		t.Fatal("calls inside a write tx must fail")
	}
	i.FailTimes = 1
	if _, err := i.Issue(ctx, d); !errors.Is(err, ErrRegister) {
		t.Fatal("injected failure")
	}
	a, _ := i.Issue(ctx, d)
	b, _ := i.Issue(ctx, d)
	if a.ExternalID == "" || a.ExternalID != b.ExternalID || a.Number != b.Number || a.Status != taxdocs.StatusIssued {
		t.Fatal("issue must be idempotent by key", a, b)
	}
	i.Async = true
	p, _ := i.Issue(ctx, taxdocs.Document{IdempotencyKey: "taxdoc:2"})
	if p.Status != taxdocs.StatusPending {
		t.Fatal(p)
	}
	if f, _ := i.Fetch(ctx, p.ExternalID); f.Status != taxdocs.StatusIssued {
		t.Fatal(f)
	}
}
