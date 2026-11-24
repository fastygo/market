// Package fakeissuer is an in-memory taxdocs.Issuer for tests and local
// development: it can fail a number of times, always fail, or answer
// pending (async register) and confirm on Fetch. Calls inside a write
// transaction fail like any network adapter.
package fakeissuer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/fastygo/market/internal/platform/tx"
	"github.com/fastygo/market/internal/taxdocs"
)

// ErrRegister is the injected failure.
var ErrRegister = errors.New("fakeissuer: register unavailable")

// Issuer is the fake DocumentIssuer.
type Issuer struct {
	mu         sync.Mutex
	FailTimes  int  // fail the next N Issue calls
	AlwaysFail bool // fail every Issue call
	Async      bool // Issue answers pending; Fetch confirms
	issued     map[string]taxdocs.Result
	calls      int
	seq        int
}

// New returns a fake issuer.
func New() *Issuer { return &Issuer{issued: map[string]taxdocs.Result{}} }

// Code implements taxdocs.Issuer.
func (i *Issuer) Code() string { return "fake" }

// Calls returns the number of Issue calls.
func (i *Issuer) Calls() int { i.mu.Lock(); defer i.mu.Unlock(); return i.calls }

// Issue implements taxdocs.Issuer; idempotent by key.
func (i *Issuer) Issue(ctx context.Context, d taxdocs.Document) (taxdocs.Result, error) {
	if err := tx.AssertNoTx(ctx); err != nil {
		return taxdocs.Result{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	if i.AlwaysFail || i.FailTimes > 0 {
		if i.FailTimes > 0 {
			i.FailTimes--
		}
		return taxdocs.Result{}, ErrRegister
	}
	sum := sha256.Sum256([]byte(d.IdempotencyKey))
	id := "fd_" + hex.EncodeToString(sum[:6])
	if r, ok := i.issued[id]; ok {
		return r, nil
	}
	i.seq++
	r := taxdocs.Result{ExternalID: id, Number: fmt.Sprintf("FD-%06d", i.seq), Status: taxdocs.StatusIssued,
		Attrs: map[string]any{"operation": d.Operation, "total_minor": d.Total}}
	if i.Async {
		r.Status = taxdocs.StatusPending
	}
	i.issued[id] = r
	return r, nil
}

// Fetch implements taxdocs.Issuer; pending documents become issued.
func (i *Issuer) Fetch(ctx context.Context, externalID string) (taxdocs.Result, error) {
	if err := tx.AssertNoTx(ctx); err != nil {
		return taxdocs.Result{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	r, ok := i.issued[externalID]
	if !ok {
		return taxdocs.Result{}, ErrRegister
	}
	r.Status = taxdocs.StatusIssued
	i.issued[externalID] = r
	return r, nil
}
