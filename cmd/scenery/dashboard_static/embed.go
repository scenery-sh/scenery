package dashboardstatic

import (
	"embed"
	"io/fs"
)

// dist is populated by scripts/build-dashboard-ui-embed.sh before release
// binaries are compiled. The tracked placeholder keeps go:embed valid in a
// fresh source checkout before the generated Vite assets exist.
//
//go:embed dist
var dist embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
