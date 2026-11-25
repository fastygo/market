package web

import (
	"github.com/ui8kit/codegen/generated/templ/compose"
	orgutils "github.com/ui8kit/codegen/generated/templ/orgutils"
)

// ClassMode is the UI8Kit class mode the embedded stylesheets are built for (ADR-0029): compositions render
// ui-* names (CSS from scripts/uisem, @apply in @layer components.kit) and the organism helpers only join
// classes, so there is no Tailwind class merge for the kit's own classes at render time. The primitives
// still merge the final class list (their generator, ui8kit/codegen, has no semantic option yet).
const ClassMode = compose.ClassesSemantic

func init() {
	compose.SetClassMode(ClassMode)
	orgutils.SetClassMode(ClassMode)
}
