// Package ui embeds the TypeScript UI catalog shipped by the Scenery binary.
package ui

import "embed"

// Files contains the editable catalog source under ui/.
//
//go:embed package.json global.d.ts index.ts tokens.stylex.ts components
var Files embed.FS
