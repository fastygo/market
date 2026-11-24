// Package tx holds the provider-neutral part of the write transaction
// discipline (ADR-0007 rule zero): an "in write transaction" flag carried in
// the context and a process-wide counter of open write transactions.
// Adapters that talk to the network (payment gateways, storage presigners,
// mailers) call AssertNoTx before any call.
package tx

import (
	"context"
	"errors"
	"sync/atomic"
)

// ErrNetworkInTx is returned when a network call is attempted while the
// caller holds a write transaction.
var ErrNetworkInTx = errors.New("tx: network call inside a write transaction (ADR-0007 rule zero)")

type ctxKey struct{}

var open atomic.Int64

// Mark returns a context flagged as running inside a write transaction.
func Mark(ctx context.Context) context.Context { return context.WithValue(ctx, ctxKey{}, true) }

// InTx reports whether ctx belongs to a write transaction.
func InTx(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKey{}).(bool)
	return v
}

// AssertNoTx fails when ctx is inside a write transaction.
func AssertNoTx(ctx context.Context) error {
	if InTx(ctx) {
		return ErrNetworkInTx
	}
	return nil
}

// Enter and Leave maintain the open write transaction counter; only the
// transaction helper of the storage adapter calls them.
func Enter() { open.Add(1) }

// Leave decrements the open write transaction counter.
func Leave() { open.Add(-1) }

// Open is the number of write transactions currently open in the process.
func Open() int64 { return open.Load() }
