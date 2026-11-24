package tx

import (
	"context"
	"errors"
	"testing"
)

func TestFlag(t *testing.T) {
	ctx := context.Background()
	if InTx(ctx) || AssertNoTx(ctx) != nil {
		t.Fatal("plain context must not be in tx")
	}
	in := Mark(ctx)
	if !InTx(in) || !errors.Is(AssertNoTx(in), ErrNetworkInTx) {
		t.Fatal("marked context must be in tx")
	}
	Enter()
	if Open() != 1 {
		t.Fatal("counter")
	}
	Leave()
	if Open() != 0 {
		t.Fatal("counter")
	}
}
