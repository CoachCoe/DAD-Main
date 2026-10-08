STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0

# Load testing against a disposable, seeded deployment; see docs/operations.md.
SEED_DONORS ?= 50000
LOAD_API ?= http://127.0.0.1:8002
LOAD_MIX ?=
LOADTEST = cd apps/donor-api && go run ./cmd/loadtest -api "$(LOAD_API)" -fixture "$(LOAD_FIXTURE)" -mix "$(LOAD_MIX)" -client-ips

.PHONY: install db-up migrate api donor-go web dev worker test test-api test-donor-go test-web test-browser-e2e lint lint-go lint-web lint-scripts check-ingress smoke-ingress generate check-generated seed loadtest-seeded deploy-build deploy-up deploy-smoke deploy-down deploy-logs

install:
	cd apps/donor-api && go mod download
	cd apps/web && npm ci

db-up:
	docker compose up -d --wait postgres

migrate:
	cd apps/donor-api && go run ./cmd/migrate

api donor-go:
	cd apps/donor-api && go run ./cmd/api

web:
	cd apps/web && npm run dev

dev:
	@echo "Run 'make api' and 'make web' in separate terminals after loading your environment."

worker:
	cd apps/donor-api && go run ./cmd/worker --kind "$(KIND)"

generate:
	docker run --rm -v "$(CURDIR)/apps/donor-api:/src" -w /src sqlc/sqlc:1.31.1 generate

check-generated: generate
	@test -z "$$(git status --porcelain -- apps/donor-api/internal/store/queries)" || \
		{ git status --porcelain -- apps/donor-api/internal/store/queries; echo "Generated queries differ from SQL sources; run make generate and commit the result." >&2; exit 1; }

test: test-donor-go test-web

test-api test-donor-go:
	cd apps/donor-api && test -z "$$(gofmt -l .)" && go vet ./... && go test -race ./... && go build ./...

test-web:
	cd apps/web && npm run lint && npm test -- --run && npm exec next typegen && npm run typecheck && npm run build

test-browser-e2e:
	cd apps/web && npm run test:e2e

lint: lint-go lint-web lint-scripts

lint-go:
	cd apps/donor-api && go run $(STATICCHECK) ./... && go run $(GOVULNCHECK) ./...

lint-web:
	cd apps/web && npm audit --omit=dev --audit-level=high

lint-scripts:
	@if command -v shellcheck >/dev/null; then shellcheck scripts/*.sh deploy/nginx/*.envsh; else echo "shellcheck not installed; skipping script lint" >&2; fi

check-ingress:
	./scripts/check-ingress.sh config

smoke-ingress:
	./scripts/check-ingress.sh smoke

seed:
	cd apps/donor-api && go run ./cmd/seed -database-url "$(SEED_DATABASE_URL)" -confirm "$(SEED_CONFIRM)" -donors "$(SEED_DONORS)" -fixture "$(LOAD_FIXTURE)"

loadtest-seeded:
	$(LOADTEST) -duration 60s -concurrency 256 -max-p99 250ms
	$(LOADTEST) -duration 5m -concurrency 32 -max-p99 100ms

deploy-build:
	./scripts/deploy.sh build

deploy-up:
	./scripts/deploy.sh up

deploy-smoke:
	./scripts/deploy.sh smoke

deploy-down:
	./scripts/deploy.sh down

deploy-logs:
	./scripts/deploy.sh logs
