package ui

import "embed"

// Dist contains the built dashboard assets generated into ui/dist.
//
//go:embed dist
var Dist embed.FS
