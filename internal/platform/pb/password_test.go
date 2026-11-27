package pb_test

import (
	"context"
	"testing"

	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
)

func TestSetPassword(t *testing.T) {
	app := pbtest.New(t)
	u := &pb.Users{App: app}
	ctx := context.Background()
	if _, _, err := u.Ensure(ctx, "smoke@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := u.SetPassword(ctx, "smoke@example.com", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := u.SetPassword(ctx, "nobody@example.com", "a-long-enough-password-1"); err == nil {
		t.Fatal("unknown account accepted")
	}
	if err := u.SetPassword(ctx, "Smoke@example.com", "a-long-enough-password-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.CheckPassword(ctx, "smoke@example.com", "a-long-enough-password-1"); err != nil {
		t.Fatal("password sign-in failed:", err)
	}
	if _, err := u.CheckPassword(ctx, "smoke@example.com", "wrong-password-xxxxxxx"); err == nil {
		t.Fatal("wrong password accepted")
	}
}
