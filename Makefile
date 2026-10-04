.PHONY: install db-up migrate api donor-go web dev worker test test-api test-donor-go test-web test-browser-e2e generate deploy-build deploy-up deploy-smoke deploy-down deploy-logs

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

test: test-donor-go test-web

test-api test-donor-go:
	cd apps/donor-api && test -z "$$(gofmt -l .)" && go vet ./... && go test -race ./... && go build ./...

test-web:
	cd apps/web && npm run lint && npm test -- --run && npm exec next typegen && npm run typecheck && npm run build

test-browser-e2e:
	cd apps/web && npm run test:e2e

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
