SHELL := /bin/sh

APP_NAME ?= maas-box
GO ?= go
NPM ?= npm
DOCKER_COMPOSE ?= docker compose

.PHONY: help fmt test build web-install web-build check-web-dist build-linux-amd64-web build-linux-arm64-web build-linux-all-web deps-up-local deps-down-local deps-up-prod deps-down-prod smoke

help:
	@echo "Targets:"
	@echo "  make fmt                   - go fmt all packages"
	@echo "  make test                  - run go tests"
	@echo "  make build                 - build local backend-only binary"
	@echo "  make web-install           - install frontend deps (lockfile mode)"
	@echo "  make web-build             - build frontend dist"
	@echo "  make build-linux-amd64-web - build linux amd64 single binary (Go+Web)"
	@echo "  make build-linux-arm64-web - build linux arm64 single binary (Go+Web)"
	@echo "  make build-linux-all-web   - build linux amd64/arm64 single binaries"
	@echo "  make deps-up-local         - start AI + ZLM containers for local development"
	@echo "  make deps-down-local       - stop local AI + ZLM containers"
	@echo "  make deps-up-prod          - start AI + ZLM containers for prod mode"
	@echo "  make deps-down-prod        - stop prod AI + ZLM containers"
	@echo "  make smoke                 - run e2e smoke script (PowerShell)"

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

build:
	$(GO) build -o $(APP_NAME) ./main.go

web-install:
	cd web && $(NPM) ci

web-build:
	cd web && $(NPM) ci && $(NPM) run build

check-web-dist:
	@if [ ! -f web/dist/index.html ]; then \
		echo "web/dist/index.html not found, run 'make web-build' first."; \
		exit 1; \
	fi

build-linux-amd64-web: check-web-dist
	mkdir -p build
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -tags embed_web -o build/maas-box-linux-amd64 ./main.go ./web_assets_embed.go

build-linux-arm64-web: check-web-dist
	mkdir -p build
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -tags embed_web -o build/maas-box-linux-arm64 ./main.go ./web_assets_embed.go

build-linux-all-web: build-linux-amd64-web build-linux-arm64-web

deps-up-local:
	$(DOCKER_COMPOSE) --env-file deploy/env/local.env -f docker-compose.ai.local.yml up -d --build
	$(DOCKER_COMPOSE) --env-file deploy/env/local.env -f docker-compose.zlm.local.yml up -d

deps-down-local:
	$(DOCKER_COMPOSE) --env-file deploy/env/local.env -f docker-compose.ai.local.yml down
	$(DOCKER_COMPOSE) --env-file deploy/env/local.env -f docker-compose.zlm.local.yml down

deps-up-prod:
	$(DOCKER_COMPOSE) --env-file deploy/env/prod.env -f docker-compose.ai.yml up -d --build
	$(DOCKER_COMPOSE) --env-file deploy/env/prod.env -f docker-compose.zlm.yml up -d

deps-down-prod:
	$(DOCKER_COMPOSE) --env-file deploy/env/prod.env -f docker-compose.ai.yml down
	$(DOCKER_COMPOSE) --env-file deploy/env/prod.env -f docker-compose.zlm.yml down

smoke:
	powershell -ExecutionPolicy Bypass -File scripts/smoke-e2e.ps1 -BaseUrl "http://127.0.0.1:15123" -Username "admin" -Password "admin" -CallbackToken "maas-box-callback-token"
