package yookassa

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/fastygo/market/internal/taxdocs"
)

// Receipts is the taxdocs.Issuer for "Receipts from YooKassa": the
// receipt is registered together with the payment (or, for a full
// refund, by YooKassa automatically), so Issue does not create anything;
// it looks the receipt up by the payment or refund id and reports its
// register status. A missing receipt is an error, so the outbox retries
// up to the cap and then raises the page alert (ADR-0008).
type Receipts struct{ G *Gateway }

var _ taxdocs.Issuer = Receipts{}

// Code implements taxdocs.Issuer.
func (Receipts) Code() string { return string(Code) }

type receiptObj struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Status               string `json:"status"`
	FiscalDocumentNumber string `json:"fiscal_document_number"`
	FiscalStorageNumber  string `json:"fiscal_storage_number"`
	FiscalAttribute      string `json:"fiscal_attribute"`
	RegisteredAt         string `json:"registered_at"`
}

// ErrNoReceipt: YooKassa has no receipt for the payment or refund yet.
var ErrNoReceipt = errors.New("yookassa: no receipt registered yet")

// Issue implements taxdocs.Issuer.
func (r Receipts) Issue(ctx context.Context, d taxdocs.Document) (taxdocs.Result, error) {
	if !validID(d.ProviderRef) {
		return taxdocs.Result{}, errors.New("yookassa: document has no provider payment or refund id")
	}
	q, typ := url.Values{"payment_id": {d.ProviderRef}}, "payment"
	if d.Kind == taxdocs.ReceiptReturn {
		q, typ = url.Values{"refund_id": {d.ProviderRef}}, "refund"
	}
	var list struct {
		Items []receiptObj `json:"items"`
	}
	if err := r.G.do(ctx, http.MethodGet, "/receipts?"+q.Encode(), "", nil, &list); err != nil {
		return taxdocs.Result{}, err
	}
	for _, it := range list.Items {
		if it.Type == typ {
			return result(it), nil
		}
	}
	return taxdocs.Result{}, ErrNoReceipt
}

// Fetch implements taxdocs.Issuer: GET /receipts/{id}.
func (r Receipts) Fetch(ctx context.Context, externalID string) (taxdocs.Result, error) {
	if !validID(externalID) {
		return taxdocs.Result{}, fmt.Errorf("yookassa: bad receipt id")
	}
	var it receiptObj
	if err := r.G.do(ctx, http.MethodGet, "/receipts/"+externalID, "", nil, &it); err != nil {
		return taxdocs.Result{}, err
	}
	return result(it), nil
}

func result(it receiptObj) taxdocs.Result {
	res := taxdocs.Result{ExternalID: it.ID, Number: it.FiscalDocumentNumber, Status: taxdocs.StatusPending,
		Attrs: map[string]any{"fiscal_storage_number": it.FiscalStorageNumber, "fiscal_attribute": it.FiscalAttribute, "registered_at": it.RegisteredAt}}
	switch it.Status {
	case "succeeded":
		res.Status = taxdocs.StatusIssued
	case "canceled":
		res.Status = taxdocs.StatusFailed
	}
	return res
}
