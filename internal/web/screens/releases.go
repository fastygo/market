package screens

import (
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/upload"
	"github.com/fastygo/market/ui/design"
)

// ReleaseQueueView is the moderator list of releases (ADR-0036).
type ReleaseQueueView struct {
	Releases []authoring.ReleaseReview
	Status   string
}

// ReleaseReviewView is one release in moderation.
type ReleaseReviewView struct {
	R      authoring.ReleaseReview
	Report upload.Report
	CSRF   string
}

// EditorReleaseHref is the moderation page of a release.
func EditorReleaseHref(id string) string { return "/editor/releases/" + id }

func releaseQueueRows(s Shell, rs []authoring.ReleaseReview) []Row {
	out := make([]Row, 0, len(rs))
	for _, r := range rs {
		meta := r.SellerName + " \u00b7 " + r.Version + " \u00b7 " + sizeText(s, r.Size) + " \u00b7 " + fmtDate(r.Created)
		row := Row{Title: r.ItemTitle, Href: EditorReleaseHref(r.ID), Meta: meta, Badge: itemStatus(s, r.Status), Tone: statusTone(r.Status)}
		if r.Flags > 0 {
			row.Lines = append(row.Lines, tpl(s.T("ui.release.flags_n"), strconv.Itoa(r.Flags)))
		}
		if r.ReviewedBy != "" {
			row.Lines = append(row.Lines, reviewedLine(s, r.ReviewedBy, r.ReviewedAt.IsZero(), fmtDate(r.ReviewedAt)))
		}
		if r.ReviewNote != "" {
			row.Lines = append(row.Lines, r.ReviewNote)
		}
		out = append(out, row)
	}
	return out
}

func reviewedLine(s Shell, by string, noDate bool, at string) string {
	if noDate {
		return tplN(s.T("ui.release.reviewed_by_nodate"), by)
	}
	return tplN(s.T("ui.release.reviewed_by"), by, at)
}

func releaseQueueActions(s Shell, rs []authoring.ReleaseReview) func(int) templ.Component {
	return func(i int) templ.Component {
		return LinkButton(EditorReleaseHref(rs[i].ID), s.T("ui.release.open_review"), "outline", "eye")
	}
}

func releaseSpecs(s Shell, v ReleaseReviewView) []design.Row {
	r := v.R
	rows := []design.Row{
		{Label: s.T("ui.release.f_item"), Value: r.ItemTitle},
		{Label: s.T("ui.release.f_vendor"), Value: r.SellerName + " (" + r.SellerSlug + ")"},
		{Label: s.T("ui.release.version"), Value: r.Version},
		{Label: s.T("ui.release.f_status"), Value: itemStatus(s, r.Status)},
		{Label: s.T("ui.release.f_file"), Value: r.Name},
		{Label: s.T("ui.release.f_size"), Value: sizeText(s, r.Size)},
		{Label: s.T("ui.release.f_sha256"), Value: r.SHA256},
		{Label: s.T("ui.release.f_uploaded"), Value: fmtDate(r.Created)},
	}
	if v.Report.Files > 0 {
		rows = append(rows, design.Row{Label: s.T("ui.release.f_files"), Value: strconv.Itoa(v.Report.Files) + " \u00b7 " + sizeText(s, v.Report.Bytes)})
	}
	if r.ReviewedBy != "" {
		rows = append(rows, design.Row{Label: s.T("ui.release.f_reviewed"), Value: reviewedLine(s, r.ReviewedBy, r.ReviewedAt.IsZero(), fmtDate(r.ReviewedAt))})
	}
	if r.ReviewNote != "" {
		rows = append(rows, design.Row{Label: s.T("ui.release.f_note"), Value: r.ReviewNote})
	}
	return rows
}

func flagItems(s Shell, fs []upload.Flag) []design.FileItem {
	out := make([]design.FileItem, 0, len(fs))
	for _, f := range fs {
		icon := "info"
		switch f.Kind {
		case upload.FlagSecret:
			icon = "key-round"
		case upload.FlagBinary, upload.FlagExecutable:
			icon = "square-terminal"
		}
		out = append(out, design.FileItem{Icon: icon, Name: f.Path, Text: s.T("ui.release.flag_"+f.Kind) + ": " + f.Rule, Kind: s.T("ui.release.flag_kind_" + f.Kind)})
	}
	return out
}

func entryItems(s Shell, es []upload.Entry) []design.FileItem {
	out := make([]design.FileItem, 0, len(es))
	for _, e := range es {
		out = append(out, design.FileItem{Icon: "file-text", Name: e.Name, Text: "", Kind: sizeText(s, e.Size)})
	}
	return out
}

func listingTitle(s Shell, rep upload.Report) string {
	t := s.T("ui.release.listing")
	if rep.Files > len(rep.Entries) {
		t += " " + tplN(s.T("ui.release.listing_first"), strconv.Itoa(len(rep.Entries)), strconv.Itoa(rep.Files))
	}
	return t
}

func releaseCanDecide(r authoring.ReleaseReview) bool {
	return r.Status == authoring.ReleaseInReview || r.Status == authoring.ReleaseDraft
}

// tplN fills {0}, {1}, ... in a dictionary message.
func tplN(msg string, v ...string) string {
	for i, x := range v {
		msg = strings.ReplaceAll(msg, "{"+strconv.Itoa(i)+"}", x)
	}
	return msg
}
