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

# Go and templ (once code exists)
if [ -f go.mod ]; then
  test -z "$(gofmt -l . | grep -v '_templ.go' || true)" || { echo "verify: gofmt"; gofmt -l .; fail=1; }
  go vet ./... || fail=1
  go test -race -count=1 ./... || fail=1
fi

[ "$fail" -eq 0 ] && echo "verify: ok"
exit "$fail"
