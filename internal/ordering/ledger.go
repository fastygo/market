package ordering

import "sort"

// Ledger accounts (ADR-0006).
const (
	AccProviderClearing = "provider_clearing"
	AccPlatformRevenue  = "platform_revenue"
	AccTaxPayable       = "tax_payable"
	AccSellerPayable    = "seller_payable"
)

// Entry is one ledger row; Seller is set only for seller_payable.
type Entry struct {
	TxnID    string
	Account  string
	Seller   string
	Amount   int64
	Currency string
	RefType  string
	RefID    string
}

// PaymentTxnID is the deterministic id of the payment succeeded txn.
func PaymentTxnID(paymentID string) string { return "pay:" + paymentID + ":succeeded" }

// PostPayment returns the balanced entries of a succeeded payment, one row
// per (account, seller) so the (txn_id, account) unique holds per seller
// within single-seller orders. Principal: clearing +line, revenue
// -(line - tax), tax_payable -tax. Agent: clearing +line, revenue
// -(commission - commission tax), tax_payable -commission tax,
// seller_payable -(line - commission). commissionTax is computed by the
// caller from the operator's TaxPolicy (0 when not registered).
func PostPayment(paymentID, currency string, lines []Line, commissionTax func(Line) int64) []Entry {
	txn := PaymentTxnID(paymentID)
	sum := map[[2]string]int64{}
	add := func(acc, seller string, v int64) { sum[[2]string{acc, seller}] += v }
	for _, l := range lines {
		total := l.LineMinor
		if l.Tax.Mode == TaxExclusive {
			total += l.TaxMinor
		}
		add(AccProviderClearing, "", total)
		if l.Offer.SaleModel == Agent {
			ct := int64(0)
			if commissionTax != nil {
				ct = commissionTax(l)
			}
			add(AccPlatformRevenue, "", -(l.Commission - ct))
			add(AccTaxPayable, "", -ct)
			add(AccSellerPayable, l.Offer.SellerID, -(total - l.Commission))
			continue
		}
		add(AccPlatformRevenue, "", -(total - l.TaxMinor))
		add(AccTaxPayable, "", -l.TaxMinor)
	}
	var out []Entry
	for k, v := range sum {
		if v == 0 && k[0] != AccProviderClearing {
			continue
		}
		out = append(out, Entry{TxnID: txn, Account: k[0], Seller: k[1], Amount: v, Currency: currency, RefType: "payment", RefID: paymentID})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Account != out[j].Account {
			return out[i].Account < out[j].Account
		}
		return out[i].Seller < out[j].Seller
	})
	return out
}

// Balanced reports whether entries sum to zero per txn and currency.
func Balanced(es []Entry) bool {
	s := map[[2]string]int64{}
	for _, e := range es {
		s[[2]string{e.TxnID, e.Currency}] += e.Amount
	}
	for _, v := range s {
		if v != 0 {
			return false
		}
	}
	return true
}
