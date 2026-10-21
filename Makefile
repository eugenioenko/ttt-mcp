.PHONY: build test lint test-editor clean

VERSION ?= dev

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o bin/ttt-mcp .

test:
	go test -race ./...

lint:
	test -z "$$(gofmt -l main.go internal plugin/*.go tests)"
	go vet ./...

test-editor:
	test -n "$(TTT_BINARY)" || (echo "Set TTT_BINARY to a ttt build with RPC and automation APIs"; exit 1)
	TTT_BINARY="$(TTT_BINARY)" go test -race ./tests -v -count=1

clean:
	rm -rf bin dist
