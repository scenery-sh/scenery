// Package js contains the SDK source distributed with the compiler.
package js

import "embed"

//go:embed scenery-sdk/*.mjs scenery-sdk/*.mts
var SDK embed.FS
