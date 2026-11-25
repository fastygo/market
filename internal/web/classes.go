package web

import (
	"github.com/ui8kit/codegen/generated/templ/compose"
	orgutils "github.com/ui8kit/codegen/generated/templ/orgutils"
	uiutils "github.com/ui8kit/codegen/generated/templ/utils"
)

// ClassMode is the UI8Kit class mode the embedded stylesheets are built for (ADR-0029): compositions render
// ui-* names (CSS from scripts/uisem, @apply in @layer components.kit), and the organism helpers (Cnv) and the
// primitives (uiutils.Cn) only join classes, so there is no Tailwind class merge for the final class lists at
// render time. Still merged: uiutils.Compose (primitives with their own recipe, Icon) and orgutils.Variant
// (organism recipes that override their own base).
const ClassMode = compose.ClassesSemantic

func init() {
	compose.SetClassMode(ClassMode)
	orgutils.SetClassMode(ClassMode)
	uiutils.SetClassMode(ClassMode)
}
