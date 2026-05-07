WAILS := $(shell which wails 2>/dev/null || echo ~/go/bin/wails)

.PHONY: help dev build build-universal build-windows test install-wails

help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "  dev              開発モード（ホットリロード付き）"
	@echo "  build            現在の環境向けビルド → build/bin/moviedl"
	@echo "  build-universal  macOS ユニバーサルバイナリ (arm64 + amd64)"
	@echo "  build-windows    Windows 向けクロスビルド (amd64)"
	@echo "  test             Go テストを実行"
	@echo "  install-wails    Wails CLI をインストール"

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
