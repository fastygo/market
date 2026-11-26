package yookassa_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/yookassa"
	"github.com/fastygo/market/internal/platform/money"
)

// TestTestShopSmoke calls the real YooKassa test shop when
// YOOKASSA_TEST_SHOP_ID and YOOKASSA_TEST_SECRET_KEY are set. It creates
// a payment, checks the confirmation URL, and fetches status. It never
// completes the payment and refuses to run with a non-test secret.
// Secret values are never logged.
func TestTestShopSmoke(t *testing.T) {
	shop := os.Getenv("YOOKASSA_TEST_SHOP_ID")
	secret := os.Getenv("YOOKASSA_TEST_SECRET_KEY")
	if shop == "" || secret == "" {
		t.Skip("YOOKASSA_TEST_SHOP_ID and YOOKASSA_TEST_SECRET_KEY are not set")
	}
	if !strings.HasPrefix(secret, "test_") {
		t.Fatal("refusing to run: YOOKASSA_TEST_SECRET_KEY is not a test-shop key")
	}
	gw, err := yookassa.New(yookassa.Config{ShopID: shop, SecretKey: secret, Receipts: false, CheckIP: false})
	if err != nil {
		t.Fatal("gateway config rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	key := "pay:smoke:market:" + time.Now().UTC().Format("20060102150405.000000000")
	res, err := gw.CreatePayment(ctx, payments.CreatePaymentRequest{
		IdempotencyKey: key,
		Amount:         money.Money{Minor: 100, Currency: "RUB"},
		Method:         "card",
		Description:    "market adapter smoke",
		ReturnURL:      "https://market.example.com/checkout/return/smoke",
		OrderID:        "smoke",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if res.ProviderPaymentID == "" || !strings.HasPrefix(res.ConfirmationURL, "https://") {
		t.Fatalf("missing id or confirmation URL (state %s)", res.Status.State)
	}
	if res.Status.State != payments.StatePending {
		t.Fatalf("created payment state %s, want pending", res.Status.State)
	}
	st, err := payments.GetStatus(ctx, gw, res.ProviderPaymentID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if st.ProviderPaymentID != res.ProviderPaymentID || st.State != payments.StatePending {
		t.Fatalf("status id=%s state=%s, want pending", st.ProviderPaymentID, st.State)
	}
	if st.Amount.Minor != 100 || !strings.EqualFold(st.Amount.Currency, "RUB") {
		t.Fatalf("status amount %d %s", st.Amount.Minor, st.Amount.Currency)
	}
}
