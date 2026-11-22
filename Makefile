GO ?= go
DSN ?= postgres://desk@127.0.0.1:5432/desk?sslmode=disable

.PHONY: check test study readme heldout replay serve console screenshots

check: ## format, vet, tests, README in step with the reports
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	$(GO) vet ./...
	$(GO) test -race -p 1 ./...
	$(GO) run ./cmd/desk readme && git diff --exit-code README.md

test:
	$(GO) test -race -p 1 ./...

study: ## the dev study (about a minute on two cores)
	$(GO) run ./cmd/desk study -world dev

readme:
	$(GO) run ./cmd/desk readme

heldout: ## once, at the end; writes reports/heldout.lock
	$(GO) run ./cmd/desk study -world heldout

replay: ## the desk over six hours into Postgres and into memory: same decisions
	$(GO) run ./cmd/desk replay -dsn '$(DSN)'

serve:
	$(GO) run ./cmd/desk serve -addr :8080

console:
	cd web && npm ci && npm test && npm run build
