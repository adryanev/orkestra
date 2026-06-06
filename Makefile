.PHONY: build test vet lint fmt all

# Mirror the CI checks for local use.
all: build vet lint test

build:
	go build ./...

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
