package main

import (
	"embed"
	"flag"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// --project <slug> deep-links directly into that project on launch.
	// The slug is the ~/.claude/projects/<slug> directory name (Claude Code
	// replaces both '/' and '_' with '-' in the cwd). Frontend reads it via
	// the GetInitialProject binding and seeds nav state.
	//
	// Example (from macOS):
	//   open -n -a claude-trace --args --project -Users-chris-code-claude-world-chief
	initialProject := flag.String("project", "", "open directly to this project slug (as in ~/.claude/projects/<slug>)")
	flag.Parse()

	// Create an instance of the app structure
	app := NewApp(*initialProject)

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "claude-trace",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
