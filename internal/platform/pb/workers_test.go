package pb_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
)

// The OnTerminate drain: after cancel, wait reports both workers done and
// no worker goroutine is left behind.
func TestRunWorkersStopAndWait(t *testing.T) {
	app := pbtest.New(t)
	svc := &ordering.Service{Store: &pb.Orders{App: app}}
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	wait := svc.RunWorkers(ctx, 10*time.Millisecond, func(err error) { t.Error(err) })
	time.Sleep(50 * time.Millisecond) // a few idle polls
	cancel()
	if !wait(2 * time.Second) {
		t.Fatal("workers did not stop")
	}
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines %d after stop, %d before", n, before)
	}
}
