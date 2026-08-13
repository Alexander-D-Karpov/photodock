BINARY_NAME=photodock
BUILD_DIR=bin

-include .deploy.env

DEPLOY_HOST    ?=
DEPLOY_STAGE   ?= ~/photodock/main
DEPLOY_DST     ?= /opt/photodock/main
DEPLOY_SERVICE ?= photodock

.PHONY: all build clean run test dev docker docker-up docker-down lint fmt fmt-check vet ci deploy

all: build

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/photodock

clean:
	rm -rf $(BUILD_DIR)
	go clean

run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

test:
	go test -race ./...

dev:
	go run ./cmd/photodock

docker:
	docker build -t photodock .

docker-up:
	docker compose up -d

docker-down:
	docker compose down

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	go fmt ./...
	gofmt -s -w .

fmt-check:
	@out=$$(gofmt -s -l .); if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

# Mirrors the GitHub Actions pipeline; run before pushing.
ci: fmt-check vet lint test build

deploy:
	@test -n "$(DEPLOY_HOST)" || { echo "DEPLOY_HOST not set — export it or add it to .deploy.env (e.g. DEPLOY_HOST=user@host)"; exit 1; }
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o main ./cmd/photodock
	scp main $(DEPLOY_HOST):$(DEPLOY_STAGE)
	ssh -t $(DEPLOY_HOST) 'sudo install -m 755 $(DEPLOY_STAGE) $(DEPLOY_DST) && sudo systemctl restart $(DEPLOY_SERVICE)'