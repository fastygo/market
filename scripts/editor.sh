#!/usr/bin/env bash
# Build or verify the Solid EditorY island (ADR-0031).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

dest=internal/web/static/editor
mode=${1:-}

if ! command -v bun >/dev/null 2>&1; then
  if [ "$mode" = "--check" ]; then
    if ls "$dest"/editory-*.js >/dev/null 2>&1 && ls "$dest"/editory-*.css >/dev/null 2>&1; then
      echo "editor: assets present (bun not installed, skipped rebuild check)"
      exit 0
    fi
    echo "editor: missing island assets in $dest"
    exit 1
  fi
  echo "editor: bun is required to build the island (https://bun.sh)"
  exit 1
fi

tmp=
cleanup() { if [ -n "${tmp:-}" ]; then rm -rf "$tmp"; fi; }
trap cleanup EXIT

build_into() {
  local out=$1
  (cd editor && bun install --frozen-lockfile && bun run build -- --outDir "$out" --emptyOutDir)
}

if [ "$mode" = "--check" ]; then
  tmp=$(mktemp -d)
  # bun/vite outDir in the config is fixed; compare by hashing current sources
  # against a rebuild into a temp dir via a one-off config override.
  (cd editor && bun install --frozen-lockfile)
  (cd editor && bunx vite build --outDir "$tmp" --emptyOutDir)
  cur_js=$(find "$dest" -name 'editory-*.js' -print | sort | head -1)
  cur_css=$(find "$dest" -name 'editory-*.css' -print | sort | head -1)
  new_js=$(find "$tmp" -name 'editory-*.js' -print | sort | head -1)
  new_css=$(find "$tmp" -name 'editory-*.css' -print | sort | head -1)
  if [ -z "$cur_js" ] || [ -z "$cur_css" ] || [ -z "$new_js" ] || [ -z "$new_css" ]; then
    echo "editor: missing island assets (run make editor)"
    exit 1
  fi
  if ! cmp -s "$cur_js" "$new_js" || ! cmp -s "$cur_css" "$new_css"; then
    echo "editor: island assets are stale (run make editor and commit)"
    exit 1
  fi
  echo "editor: assets up to date"
  exit 0
fi

(cd editor && bun install && bun run build)
# drop vite leftover index or maps if any
find "$dest" -type f ! -name 'editory-*' -delete 2>/dev/null || true
if ! ls "$dest"/editory-*.js >/dev/null 2>&1 || ! ls "$dest"/editory-*.css >/dev/null 2>&1; then
  echo "editor: build produced no editory-* assets"
  exit 1
fi
echo "editor: wrote $(ls "$dest"/editory-*)"
