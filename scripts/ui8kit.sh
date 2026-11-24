#!/usr/bin/env bash
# Installs the UI8Kit templ kit into ui/kit from the registry with the pinned CLI.
# ui/kit is generated code: never edit it by hand; rerun this script.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)/ui/kit"
CLI="ui8kit@3.5.0"
ITEMS=(block box stack group grid text title link button link-button image lucide-icon badge container
  list list-item card inline separator product-card product-grid page-crumbs
  form input label field)
npx -y "$CLI" add --yes --force "${ITEMS[@]}"
cd ../.. && templ generate && (cd ui/kit && go build ./...)
echo "ui8kit: installed ${#ITEMS[@]} items into ui/kit"
