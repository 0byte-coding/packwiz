package main

import (
	// Modules of packwiz
	"github.com/0byte-coding/packwiz/cmd"
	_ "github.com/0byte-coding/packwiz/curseforge"
	_ "github.com/0byte-coding/packwiz/github"
	_ "github.com/0byte-coding/packwiz/migrate"
	_ "github.com/0byte-coding/packwiz/modrinth"
	_ "github.com/0byte-coding/packwiz/settings"
	_ "github.com/0byte-coding/packwiz/url"
	_ "github.com/0byte-coding/packwiz/utils"
)

func main() {
	cmd.Execute()
}
