package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The order cookie lets the browser that placed an order see that order
// on the provider's return page without signing in: it is bound to one
// order id, signed with the cabinet key, expires in 24 h, is httpOnly and
// sent only to /checkout/return/. It grants the paid items of that order
// (signed /dl links), never a session.
const (
	orderCookie    = "market_order"
	orderCookieTTL = 24 * time.Hour
	orderPath      = "/checkout/return/"
)

func (c *Cabinet) orderMAC(orderID string, exp int64) string {
	m := hmac.New(sha256.New, c.CSRFKey)
	m.Write([]byte("order-return\n" + orderID + "\n" + strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (c *Cabinet) setOrderCookie(w http.ResponseWriter, orderID string, now time.Time) {
	if len(c.CSRFKey) == 0 || orderID == "" {
		return
	}
	exp := now.Add(orderCookieTTL).Unix()
	http.SetCookie(w, &http.Cookie{Name: orderCookie, Value: orderID + "." + strconv.FormatInt(exp, 10) + "." + c.orderMAC(orderID, exp),
		Path: orderPath, MaxAge: int(orderCookieTTL.Seconds()), HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode})
}

// placedOrder reports whether this browser placed the order (valid,
// unexpired cookie for exactly this order id).
func (c *Cabinet) placedOrder(r *http.Request, orderID string, now time.Time) bool {
	if len(c.CSRFKey) == 0 || orderID == "" {
		return false
	}
	ck, err := r.Cookie(orderCookie)
	if err != nil {
		return false
	}
	parts := strings.Split(ck.Value, ".")
	if len(parts) != 3 || parts[0] != orderID {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(parts[2]), []byte(c.orderMAC(orderID, exp)))
}
