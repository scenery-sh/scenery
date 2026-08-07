package eve

import "embed"

// The scaffold lock and package manifest are the checked-in, authoritative
// dependency contract used by `scenery assistant init`. Keep these bytes
// independent from the developer's workspace and return copies so callers
// cannot mutate the embedded data shared by concurrent CLI requests.
//
//go:embed testdata/project/package.json testdata/project/package-lock.json
var scaffoldPackageFiles embed.FS

// ScaffoldPackageFiles returns the exact package manifest and lock used by the
// pinned assistant adapter fixture. The returned map and byte slices are
// caller-owned.
func ScaffoldPackageFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range []string{"package.json", "package-lock.json"} {
		data, err := scaffoldPackageFiles.ReadFile("testdata/project/" + name)
		if err != nil {
			return nil, err
		}
		files[name] = append([]byte(nil), data...)
	}
	return files, nil
}
