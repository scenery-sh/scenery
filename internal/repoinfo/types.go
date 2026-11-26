package repoinfo

// PackageInfo identifies one Go package within the repository.
type PackageInfo struct {
	ImportPath string
	Dir        string
	RelDir     string
}
