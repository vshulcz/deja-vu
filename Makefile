BINARY := deja
CMD := ./cmd/deja
PREFIX ?= /usr/local

.PHONY: build test lint install release-dry demo

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install $(CMD)

release-dry:
	goreleaser release --snapshot --clean

demo:
	DEJA_CLAUDE_ROOT=$$(pwd)/fixtures/synthetic/claude \
	DEJA_CODEX_ROOT=$$(pwd)/fixtures/synthetic/codex \
	DEJA_OPENCODE_DB=$$(pwd)/fixtures/synthetic/opencode.db \
	DEJA_INDEX_DIR=$$(mktemp -d)/deja-index.db \
	go run $(CMD) --rebuild frobnicator
