# Common tasks. Run `make help` for the list.
.DEFAULT_GOAL := help
.PHONY: help dev-backend dev-frontend build test test-backend test-frontend run docker clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'

dev-backend: ## Run the Go API on :8080 with live seed data
	cd backend && go run .

dev-frontend: ## Run the Vite dev server on :5173 (proxies /api to :8080)
	cd frontend && npm run dev

build: ## Build the frontend, then a single binary that serves it
	cd frontend && npm ci && npm run build
	cd backend && CGO_ENABLED=0 go build -trimpath -o sequencer .

run: build ## Build then serve API + frontend from one process on :8080
	cd backend && STATIC_DIR=../frontend/dist ./sequencer

test: test-backend test-frontend ## Run every test

test-backend: ## Go tests with the race detector
	cd backend && go vet ./... && go test -race ./...

test-frontend: ## Frontend unit and render tests
	cd frontend && npm test

docker: ## Build and run the container on :8080
	docker compose up --build

clean: ## Remove build output and local state
	rm -rf frontend/dist backend/sequencer backend/data
