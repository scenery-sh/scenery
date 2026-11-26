package pythonsdk

import "embed"

// SDK holds the Python Worker ABI runtime sources embedded into generated
// Python contract packages.
//
//go:embed scenery_sdk/*.py
var SDK embed.FS
