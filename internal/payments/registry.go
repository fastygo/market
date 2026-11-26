package payments

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Rule selects a provider when every set field matches the checkout
// criteria. Empty fields are wildcards.
type Rule struct {
	Currency string
	Country  string // buyer country
	Entity   string // legal entity code, e.g. "it_garage"
	Method   Method
	Provider ProviderCode
}

// Criteria is what checkout knows when choosing a provider.
type Criteria struct {
	Currency string
	Country  string
	Entity   string
	Method   Method
}

func (r Rule) match(c Criteria) bool {
	if r.Currency != "" && !eqFold(r.Currency, c.Currency) {
		return false
	}
	if r.Country != "" && !eqFold(r.Country, c.Country) {
		return false
	}
	if r.Entity != "" && !eqFold(r.Entity, c.Entity) {
		return false
	}
	if r.Method != "" && r.Method != c.Method {
		return false
	}
	return true
}

func eqFold(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

// Registry is the in-process set of enabled payment adapters (ADR-0033).
type Registry struct {
	gateways map[ProviderCode]Gateway
	rules    []Rule
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{gateways: map[ProviderCode]Gateway{}}
}

// Add registers an adapter. A later Add with the same code replaces it.
func (r *Registry) Add(g Gateway) {
	if r.gateways == nil {
		r.gateways = map[ProviderCode]Gateway{}
	}
	r.gateways[g.Code()] = g
}

// SetRules replaces the selection rules. The first matching rule whose
// provider is enabled wins.
func (r *Registry) SetRules(rules []Rule) { r.rules = append([]Rule(nil), rules...) }

// Filter keeps only the named providers. A nil or empty list leaves the
// registry unchanged (each adapter is enabled by its own credentials).
func (r *Registry) Filter(allow []string) []string {
	if len(allow) == 0 {
		return nil
	}
	set := map[ProviderCode]struct{}{}
	var missing []string
	for _, c := range allow {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		code := ProviderCode(c)
		set[code] = struct{}{}
		if _, ok := r.gateways[code]; !ok {
			missing = append(missing, c)
		}
	}
	if len(set) == 0 {
		return missing
	}
	for code := range r.gateways {
		if _, ok := set[code]; !ok {
			delete(r.gateways, code)
		}
	}
	return missing
}

// Gateway returns an enabled adapter by code.
func (r *Registry) Gateway(code ProviderCode) (Gateway, bool) {
	if r == nil {
		return nil, false
	}
	g, ok := r.gateways[code]
	return g, ok
}

// Map is a copy of the enabled adapters, keyed by code, for callers that
// still hold a map (ordering.Service.Gateways, start-up checks).
func (r *Registry) Map() map[string]Gateway {
	out := map[string]Gateway{}
	if r == nil {
		return out
	}
	for c, g := range r.gateways {
		out[string(c)] = g
	}
	return out
}

// Select returns the first rule that matches and whose provider is
// enabled. ok is false when no rule matches; the caller then falls back
// to the storefront method map.
func (r *Registry) Select(c Criteria) (Gateway, ProviderCode, bool) {
	if r == nil {
		return nil, "", false
	}
	for _, rule := range r.rules {
		if !rule.match(c) {
			continue
		}
		if g, ok := r.gateways[rule.Provider]; ok {
			return g, rule.Provider, true
		}
	}
	return nil, "", false
}

// ParseProviders splits a comma-separated allow-list.
func ParseProviders(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseRules parses MARKET_PAYMENT_RULES. Rules are separated by
// semicolons or newlines. Each rule is "key=value,key=value:provider".
// Keys: currency, country, entity, method. The last colon starts the
// provider code.
func ParseRules(s string) ([]Rule, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var parts []string
	for _, chunk := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		chunk = strings.TrimSpace(chunk)
		if chunk != "" {
			parts = append(parts, chunk)
		}
	}
	var rules []Rule
	for i, part := range parts {
		colon := strings.LastIndex(part, ":")
		if colon < 0 {
			return nil, fmt.Errorf("payments: rule %d %q: missing :provider", i+1, part)
		}
		code := ProviderCode(strings.ToLower(strings.TrimSpace(part[colon+1:])))
		if code == "" {
			return nil, fmt.Errorf("payments: rule %d %q: empty provider", i+1, part)
		}
		rule := Rule{Provider: code}
		kv := strings.TrimSpace(part[:colon])
		if kv != "" {
			for _, field := range strings.Split(kv, ",") {
				field = strings.TrimSpace(field)
				if field == "" {
					continue
				}
				k, v, ok := strings.Cut(field, "=")
				if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
					return nil, fmt.Errorf("payments: rule %d %q: expected key=value, got %q", i+1, part, field)
				}
				k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
				switch k {
				case "currency":
					rule.Currency = strings.ToUpper(v)
				case "country":
					rule.Country = strings.ToUpper(v)
				case "entity":
					rule.Entity = strings.ToLower(v)
				case "method":
					rule.Method = Method(strings.ToLower(v))
				default:
					return nil, fmt.Errorf("payments: rule %d %q: unknown field %q", i+1, part, k)
				}
			}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// HandleWebhook verifies a notification (ParseWebhook has no side
// effects) and records it through accept, which must persist the event
// before any apply. accept returns (duplicate, error).
func HandleWebhook(ctx context.Context, g Gateway, r *http.Request, accept func(context.Context, string, Event) (bool, error)) (Event, bool, error) {
	ev, err := g.ParseWebhook(ctx, r)
	if err != nil {
		return Event{}, false, err
	}
	dup, err := accept(ctx, string(g.Code()), ev)
	return ev, dup, err
}

// GetStatus is FetchPayment: the provider is the source of truth.
func GetStatus(ctx context.Context, g Gateway, providerPaymentID string) (Status, error) {
	return g.FetchPayment(ctx, providerPaymentID)
}
