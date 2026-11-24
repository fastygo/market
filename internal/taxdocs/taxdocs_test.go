package taxdocs

import (
	"errors"
	"testing"
)

type pol struct{}

func (pol) ForSale() (Kind, string, bool)         { return Receipt, "income", true }
func (pol) ForRefund() (Kind, string, bool)       { return ReceiptReturn, "income_return", true }
func (pol) LineCodes(agent bool) (string, string) { return "service", "full_payment" }

func TestBuild(t *testing.T) {
	src := []SourceLine{{Title: "A", Qty: 1, Price: 290000, Amount: 290000, TaxMode: "inclusive", TaxCode: "vat22", TaxMinor: 52295}}
	d, err := Build(pol{}, Receipt, "income", "RUB", "b@example.com", 290000, src)
	if err != nil || len(d.Lines) != 1 || d.Lines[0].Subject != "service" || d.Lines[0].Amount != 290000 {
		t.Fatal(d, err)
	}
	if _, err := Build(pol{}, Receipt, "income", "RUB", "", 1, src); !errors.Is(err, ErrMismatch) {
		t.Fatal("mismatch must fail")
	}
	ex := []SourceLine{{Qty: 1, Price: 10000, Amount: 10000, TaxMode: "exclusive", TaxMinor: 2000}}
	if d, err := Build(pol{}, Invoice, "", "EUR", "", 12000, ex); err != nil || d.Lines[0].Amount != 12000 {
		t.Fatal(d, err)
	}
}
