package tray

import _ "embed"

//go:embed icon.png
var idlePNG []byte

//go:embed active.png
var activePNG []byte

//go:embed warning.png
var warningPNG []byte

func iconBytes() []byte        { return idlePNG }
func iconActiveBytes() []byte  { return activePNG }
func iconWarningBytes() []byte { return warningPNG }
