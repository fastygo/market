# Build-time tooling only; the runtime is the single Go binary (slo.md).
TEMPL ?= templ
BIN   ?= bin/market

.PHONY: all generate uisem css editor editor-check ui8kit build test run verify clean

all: generate css editor build test

generate:            ## templ -> *_templ.go (app screens and the installed kit, from the repo root)
	$(TEMPL) generate

SKINS ?= default godudy storeee

uisem:               ## semantic kit CSS (ADR-0029): ui/styles/generated/*, .ui8px/patterns.json from ui/kit/compose/compose.json
	go run ./scripts/uisem

css: uisem           ## Tailwind v4: one stylesheet per skin -> internal/web/static/skins/<skin>.css (needs npm ci once)
	for s in $(SKINS); do npx --no-install tailwindcss -i ui/styles/entries/$$s.css -o internal/web/static/skins/$$s.css --minify || exit 1; done

ui8kit:              ## reinstall the pinned UI8Kit templ kit from the registry
	scripts/ui8kit.sh

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/market

test:
	go test -race -count=1 ./...

run: build           ## local run on 127.0.0.1:8090 with data in ./pb_data
	./$(BIN) serve --http 127.0.0.1:8090

verify:
	scripts/verify.sh

editor:              ## Solid EditorY island -> internal/web/static/editor/ (ADR-0031; needs bun)
	scripts/editor.sh

editor-check:        ## fail if committed editor assets are stale
	scripts/editor.sh --check

clean:
	rm -rf bin
