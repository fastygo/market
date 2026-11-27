package screens

import (
	"github.com/a-h/templ"
	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/ui/design"
)

// RolesView is the role overview and applications of the user.
type RolesView struct {
	P    cabinet.Principal
	Apps []cabinet.Application
	CSRF string
}

func purchases(s Shell, os []account.Order) []design.Purchase {
	var out []design.Purchase
	for _, o := range os {
		for _, it := range o.Items {
			p := design.Purchase{ID: o.ID + it.Slug, Title: it.Title, Cover: coverSrc(""), License: licenseLabel(s, it.License), Stack: it.Stack,
				Number: o.Number, Date: fmtDate(o.Created), Status: o.Status, StatusLabel: s.T(orderStatusKey(o.Status)),
				Action: s.T("ui.account.open_order"), Href: OrderHref(o.Number)}
			if it.Entitlement == "active" {
				p.Status, p.Action, p.Href = "ready", s.T("ui.product.download"), ProductHref(it.Slug)+"/download"
			}
			out = append(out, p)
		}
	}
	return out
}

func orderRows(s Shell, o account.Order) []design.Row {
	return []design.Row{
		{Label: s.T("ui.account.order"), Value: o.Number},
		{Label: s.T("ui.account.date"), Value: fmtDate(o.Created)},
		{Label: s.T("ui.account.status"), Value: s.T(orderStatusKey(o.Status))},
		{Label: s.T("ui.checkout.total"), Value: Money(o.Total)},
	}
}

func itemRows(s Shell, o account.Order) []Row {
	out := make([]Row, 0, len(o.Items))
	for _, it := range o.Items {
		out = append(out, Row{Title: it.Title, Href: ProductHref(it.Slug), Meta: licenseTitle(s, it.License), Badge: s.T(accessKey(it.Entitlement)),
			Tone: tone(it.Entitlement == "active")})
	}
	return out
}

func tone(ok bool) string {
	if ok {
		return "success"
	}
	return "neutral"
}

func statusTone(st string) string {
	switch st {
	case "published", "approved", "paid", "active", "fulfilled":
		return "success"
	case "in_review", "pending", "requested", "pending_review":
		return "warning"
	case "rejected", "cancelled", "suspended", "refunded", "disputed":
		return "destructive"
	}
	return "neutral"
}

func appRows(s Shell, as []cabinet.Application) []Row {
	out := make([]Row, 0, len(as))
	for _, a := range as {
		r := Row{Title: s.T("ui.role." + a.Role), Meta: fmtDate(a.Created), Badge: s.T("ui.status." + a.Status), Tone: statusTone(a.Status)}
		if a.StoreName != "" {
			r.Lines = append(r.Lines, a.StoreName+" ("+a.StoreSlug+")")
		}
		if a.DecisionNote != "" {
			r.Lines = append(r.Lines, a.DecisionNote)
		}
		out = append(out, r)
	}
	return out
}

func pendingRole(as []cabinet.Application, role string) bool {
	for _, a := range as {
		if a.Role == role && a.Status == "pending" {
			return true
		}
	}
	return false
}

func orderItemActions(s Shell, o account.Order) func(int) templ.Component {
	return func(i int) templ.Component { return itemAccess(s, o.Items[i]) }
}
