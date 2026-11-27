package screens

import (
	"strconv"
	"strings"
	"sync/atomic"

	uiutils "github.com/ui8kit/codegen/generated/templ/orgutils"
)

// moneyLocale is the instance UI locale (one locale per deployment,
// ADR-0018); "ru" switches prices to the Russian format.
var moneyLocale atomic.Value

// SetMoneyLocale selects how prices are written for the instance locale.
func SetMoneyLocale(locale string) { moneyLocale.Store(locale) }

var currencySigns = map[string]string{"RUB": "\u20bd", "USD": "$", "EUR": "\u20ac", "KZT": "\u20b8", "BYN": "Br"}

// formatMinor writes minor units for the instance locale. Russian: groups
// of three split by a narrow no-break space, a decimal comma only when there
// are kopecks, and the currency sign after the amount ("1 490 RUB sign").
func formatMinor(minor int64, currency string) string {
	if l, _ := moneyLocale.Load().(string); l != "ru" {
		return uiutils.Money(float64(minor), currency)
	}
	neg := minor < 0
	if neg {
		minor = -minor
	}
	whole, frac := minor/100, minor%100
	digits := strconv.FormatInt(whole, 10)
	var b strings.Builder
	if neg {
		b.WriteString("-")
	}
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString("\u202f")
		}
		b.WriteRune(r)
	}
	if frac != 0 {
		b.WriteString(",")
		if frac < 10 {
			b.WriteString("0")
		}
		b.WriteString(strconv.FormatInt(frac, 10))
	}
	sign := currencySigns[currency]
	if sign == "" {
		sign = currency
	}
	b.WriteString("\u00a0" + sign)
	return b.String()
}
