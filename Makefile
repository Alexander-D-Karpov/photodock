BINARY_NAME=photodock
BUILD_DIR=bin

.PHONY: all build clean run test dev docker

all: build

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/photodock

clean:
	rm -rf $(BUILD_DIR)
	go clean

run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

test:
	go test -v ./...

dev:
	go run ./cmd/photodock

docker:
	docker build -t photodock .

docker-up:
	docker compose up -d

docker-down:
	docker compose down

lint:
	golangci-lint run

fmt:
	go fmt ./...
	gofmt -s -w .