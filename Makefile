# pepper を埋め込んでビルドする。例: make build PEPPER=$(openssl rand -hex 16)
PEPPER ?=

.PHONY: build test
build:
	go build -ldflags "-X github.com/ktat/agentarium/kernel/secrets.pepper=$(PEPPER)" -o bin/agentarium ./cmd/agentarium

test:
	go test -race ./...
