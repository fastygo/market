#!/usr/bin/env bash
# Local gate. Run before every push. See .project/adr/0013 and 0016.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail=0
files() { git ls-files -z --cached --others --exclude-standard -- "$@"; }

hit() { # $1 label, $2 extended regex, $3 extra grep flags, $4.. pathspec
  local label=$1 re=$2 flags=${3:-} out
  shift 3 2>/dev/null || shift $#
  out=$(files "$@" | xargs -0 -r grep -nIE $flags -e "$re" -- 2>/dev/null | grep -v '^scripts/verify.sh:' || true)
  if [ -n "$out" ]; then
    echo "verify: $label"; echo "$out"; fail=1
  fi
}

# ADR-0016: hygiene checks (always on)
hit "private key block" '-----BEGIN [A-Z ]*PRIVATE KEY-----' ""
hit "API key (Stripe, PayPal, AWS, generic live keys)" '(sk|rk|pk)_(live|test)_[A-Za-z0-9]{8,}|AKIA[0-9A-Z]{16}|access_token\$production\$' ""
hit "legal id with digits" '(inn|ogrn|ogrnip|kpp|iban|bik|vat_id)["'"'"' ]*[:=][ "'"'"']*[0-9]{6,}' -i
hit "phone number" '\+[1-9][0-9]{9,14}\b' ""
emails=$(files | xargs -0 -r grep -noIE '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}' -- 2>/dev/null \
  | grep -v '^scripts/verify.sh:' \
  | grep -viE '@(example\.(com|org|net)|users\.noreply\.github\.com)$' || true)
if [ -n "$emails" ]; then echo "verify: email outside reserved domains"; echo "$emails"; fail=1; fi
envs=$(files | tr '\0' '\n' | grep -E '(^|/)\.env(\..+)?$' | grep -vE '\.example$' || true)
if [ -n "$envs" ]; then echo "verify: committed env file"; echo "$envs"; fail=1; fi
cyr=$(files | xargs -0 -r grep -nIP '[\x{0400}-\x{04FF}]' -- 2>/dev/null || true)
if [ -n "$cyr" ]; then echo "verify: Cyrillic in repo"; echo "$cyr"; fail=1; fi

# ADR-0011: goja hooks must not touch money collections
if [ -d pb_hooks ]; then
  hit "goja hook touches money collection" '(orders|order_items|payments|payment_events|refunds|tax_documents|ledger_entries|entitlements|downloads|outbox|payouts)' -w pb_hooks
fi

# UI8Kit rules on app screens (rule 02 raw tags; rule 04 literal text). The document
# shell (html, head, meta, title, link, script, body) in layout.templ has no primitive.
if [ -d internal/web/screens ]; then
  raw=$(grep -nE '<[a-z][a-z0-9]*[ />]' internal/web/screens/*.templ \
    | grep -vE '^internal/web/screens/layout\.templ:[0-9]+:\s*</?(html|head|meta|title|link|script|body)[ >/]' || true)
  if [ -n "$raw" ]; then echo "verify: raw HTML tag in screens (use UI8Kit primitives)"; echo "$raw"; fail=1; fi
  lit=$(grep -nE '^\s+[A-Za-z][A-Za-z ,.!?]*$' internal/web/screens/*.templ \
    | grep -vE ':\s+(else|default|case|return|break|continue)$' || true)
  if [ -n "$lit" ]; then echo "verify: literal text in screens (use the dictionary)"; echo "$lit"; fail=1; fi
fi

# Go and templ (once code exists)
if [ -f go.mod ]; then
  if command -v templ >/dev/null 2>&1; then
    templ generate >/dev/null 2>&1
    stale=$(git diff --name-only -- '*_templ.go' || true)
    if [ -n "$stale" ]; then echo "verify: *_templ.go out of date (run make generate and commit)"; echo "$stale"; fail=1; fi
  fi
  # ADR-0029: the generated semantic kit CSS matches ui/kit/compose/compose.json and ui/styles/kit.css
  if [ -x node_modules/.bin/tailwindcss ]; then
    go run ./scripts/uisem -check || { echo "verify: semantic kit CSS out of date (run make css and commit)"; fail=1; }
  else
    echo "verify: semantic kit CSS not verified (npm ci for the Tailwind CLI)"
  fi
  unformatted=$(gofmt -l cmd internal scripts/uisem ui/dict | grep -v '_templ.go' || true)
  test -z "$unformatted" || { echo "verify: gofmt"; echo "$unformatted"; fail=1; }
  go vet ./... || fail=1
  go test -race -count=1 ./... || fail=1
fi

[ "$fail" -eq 0 ] && echo "verify: ok"
exit "$fail"
