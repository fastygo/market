package screens

import (
	"net/url"
	"strings"
	"time"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/platform/money"

	uiutils "github.com/ui8kit/codegen/generated/templ/orgutils"
)

// SignInView is the sign-in page state.
type SignInView struct {
	CSRF, Email, Next string
	ErrorKey, InfoKey string
}

// AccountView is the signed-in cabinet frame.
type AccountView struct{ Email, CSRF string }

// CheckoutView is the product page buy form.
type CheckoutView struct {
	Enabled, Signed  bool
	CSRF, Key, Terms string
	Email, ErrorKey  string
}

// ReturnView is the provider return page.
type ReturnView struct {
	State     string // paid, pending, failed
	OrderHref string // only for the signed-in owner
}

// FakePayView is the development provider page.
type FakePayView struct {
	ID     string
	Amount money.Money
}

// OrderHref is the cabinet URL of an order.
func OrderHref(number string) string { return "/account/orders/" + number }

func orderStatusKey(st string) string { return "ui.order.status." + st }

func accessKey(ent string) string {
	switch ent {
	case "active", "refunded", "disputed", "revoked":
		return "ui.account.access." + ent
	}
	return "ui.account.access.none"
}

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func returnKey(state string) string { return "ui.checkout.return_" + state }

func strPtr(s string) *string { return &s }

func licenseTitle(s Shell, l string) string {
	return s.T("ui.account.license") + ": " + strings.ToUpper(l[:min(1, len(l))]) + l[min(1, len(l)):]
}

var _ = account.Order{}

// PriceLabel is the catalog price, or the "Free" label for a 0-amount offer.
func PriceLabel(s Shell, p catalog.Product) string {
	if !p.HasOffer {
		return ""
	}
	if p.Price.Minor == 0 {
		return s.T("ui.checkout.free")
	}
	return uiutils.Money(float64(p.Price.Minor), p.Price.Currency)
}

// IsFree is true when the product has a 0-amount offer.
func IsFree(p catalog.Product) bool {
	return p.HasOffer && p.Price.Minor == 0
}

// FreeSignInHref is the sign-in page with a return to the product.
func FreeSignInHref(slug string) string {
	return "/account?next=" + url.QueryEscape("/products/"+slug)
}
