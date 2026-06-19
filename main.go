package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend
var assets embed.FS

// version / buildDate はビルド時に -ldflags で注入する（aidlc-docs/inception/application-design/design.md 参照）。
var (
	version   = "dev"
	buildDate = ""
)

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "moviedl",
		Width:  900,
		Height: 620,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 23, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
