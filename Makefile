WAILS := $(shell which wails 2>/dev/null || echo ~/go/bin/wails)
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@latest

.PHONY: help dev build build-universal build-windows test check fmt fmtcheck vet staticcheck install-hooks install-wails

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "  dev              開発モード（ホットリロード付き）"
	@echo "  build            現在の環境向けビルド → build/bin/moviedl"
	@echo "  build-universal  macOS ユニバーサルバイナリ (arm64 + amd64)"
	@echo "  build-windows    Windows 向けクロスビルド (amd64)"
	@echo "  check            PR 前チェック一式（gofmt + vet + staticcheck + test）"
	@echo "  fmt              gofmt -w でフォーマットを自動修正"
	@echo "  test             Go テストを実行"
	@echo "  install-hooks    git の pre-push フックを有効化（make check を自動実行）"
	@echo "  install-wails    Wails CLI をインストール"

# PR を出す前に通すチェック一式。CI（.github/workflows/ci.yml）と同じ内容。
check: fmtcheck vet staticcheck test

fmt:
	gofmt -w .

# 未整形ファイルがあれば失敗させる（CI / フック用）。
fmtcheck:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt が必要なファイルがあります（make fmt で修正）:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	go vet ./...

staticcheck:
	go run $(STATICCHECK) ./...

install-hooks:
	git config core.hooksPath .githooks
	@echo "pre-push フックを有効化しました（.githooks/pre-push）"

dev:
	$(WAILS) dev

build:
	$(WAILS) build

build-universal:
	$(WAILS) build -platform darwin/universal

build-windows:
	$(WAILS) build -platform windows/amd64

test:
	go test ./...

install-wails:
	go install github.com/wailsapp/wails/v2/cmd/wails@latest
