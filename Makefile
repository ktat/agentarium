# pepper を埋め込んでビルドする。例: make build PEPPER=$(openssl rand -hex 16)
PEPPER ?=

GOLANGCI_LINT ?= $(shell go env GOPATH)/bin/golangci-lint
DENO ?= deno

.PHONY: build test lint lint-go lint-js check hooks
build:
	go build -ldflags "-X github.com/ktat/agentarium/kernel/secrets.pepper=$(PEPPER)" -o bin/agentarium ./cmd/agentarium

test:
	go test -race ./...

# Go + フロント JS の静的解析をまとめて実行
lint: lint-go lint-js

# push 前チェック（lint + test）。pre-push hook が呼ぶ
check: lint test

# Go 静的解析（staticcheck 等を含む。設定は .golangci.yml）
# 初回: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.1
#   （lint 結果はバージョンで変わるため検証済みバージョンに固定する。更新時はここも上げる）
lint-go:
	$(GOLANGCI_LINT) run ./...

# フロント JS の静的解析（設定は deno.json）
# 初回: curl -fsSL https://deno.land/install.sh | sh -s v2.8.3  （検証済みバージョン）
lint-js:
	$(DENO) lint

# git hook を有効化（push 前に make check = lint + test を実行する .githooks/pre-push）
hooks:
	git config core.hooksPath .githooks
	@echo "git hook を有効化しました（push 前に make check = lint + test を実行）。スキップ: git push --no-verify"
