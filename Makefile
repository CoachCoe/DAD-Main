GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0
SQLC := sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537
GENERATED := apps/donor-api/internal/store/queries apps/web/lib/openapi.d.ts

# Load testing against a disposable, seeded deployment; see docs/operations.md.
SEED_DONORS ?= 50000
LOAD_API ?= http://127.0.0.1:8002
LOAD_MIX ?=
# Must equal the API's DONOR_PUBLIC_ORIGIN, or referral captures get 403.
LOAD_ORIGIN ?= http://localhost:3000
LOADTEST = cd apps/donor-api && go run ./cmd/loadtest -api "$(LOAD_API)" -fixture "$(LOAD_FIXTURE)" -mix "$(LOAD_MIX)" -origin "$(LOAD_ORIGIN)" -client-ips

.PHONY: install db-up migrate provision api donor-go web dev worker reconcile test test-api test-donor-go test-web test-browser-e2e lint lint-go lint-web lint-scripts check-ingress smoke-ingress check-images generate check-generated seed loadtest-seeded deploy-build deploy-up deploy-smoke deploy-down deploy-logs

install:
	cd apps/donor-api && go mod download
	cd apps/web && npm ci

db-up:
	docker compose up -d --wait postgres

migrate:
	cd apps/donor-api && go run ./cmd/migrate

provision:
	cd apps/donor-api && go run ./cmd/provision

api donor-go:
	cd apps/donor-api && go run ./cmd/api

web:
	cd apps/web && npm run dev

dev:
	@echo "Run 'make api' and 'make web' in separate terminals after loading your environment."

worker:
	cd apps/donor-api && go run ./cmd/worker --kind "$(KIND)"

reconcile:
	cd apps/donor-api && go run ./cmd/reconcile $(RECONCILE_ARGS)

generate:
	docker run --rm -v "$(CURDIR)/apps/donor-api:/src" -w /src $(SQLC) generate
	cd apps/web && npm run --silent generate:api

check-generated: generate
	@test -z "$$(git status --porcelain -- $(GENERATED))" || \
		{ git status --porcelain -- $(GENERATED); echo "Generated code differs from its sources; run make generate and commit the result." >&2; exit 1; }

test: test-donor-go test-web

test-api test-donor-go:
	cd apps/donor-api && test -z "$$(gofmt -l .)" && go vet ./... && go test -race ./... && go build ./...

test-web:
	cd apps/web && npm run lint && npm test -- --run && npm exec next typegen && npm run typecheck && npm run build

test-browser-e2e:
	cd apps/web && npm run test:e2e

lint: lint-go lint-web lint-scripts

# staticcheck is pinned in apps/donor-api/tools/go.mod with a newer x/tools than its
# release requires, which is what reads the export data of the Go release CI uses.
lint-go:
	cd apps/donor-api && go tool -modfile=tools/go.mod staticcheck ./... && go run $(GOVULNCHECK) ./...

lint-web:
	cd apps/web && npm audit --omit=dev --audit-level=high

lint-scripts:
	@if command -v shellcheck >/dev/null || [ -n "$$CI" ]; then shellcheck scripts/*.sh deploy/nginx/*.envsh; else echo "shellcheck not installed; skipping script lint" >&2; fi

check-ingress:
	./scripts/check-ingress.sh config

smoke-ingress:
	./scripts/check-ingress.sh smoke

# The web image is built for the ingress check's hosts so it can be served through nginx.
check-images:
	docker build -f apps/donor-api/Dockerfile -t impactgraph/api:check .
	docker run --rm --entrypoint sh impactgraph/api:check -c \
		'for binary in api worker migrate provision monitor reconcile staff rekey; do command -v "$$binary" >/dev/null || exit 1; done'
	docker build -f apps/web/Dockerfile -t impactgraph/web:check \
		--build-arg DONOR_PUBLIC_ORIGIN=https://public.test --build-arg DONOR_STAFF_ORIGIN=https://staff.test .
	./scripts/check-ingress.sh web impactgraph/web:check

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
