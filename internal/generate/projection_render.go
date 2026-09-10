package generate

// renderExpectedGoPackageFiles is the shared app-imported projection. Build
// workspaces add private adapters/composition to these exact package bytes.
func renderExpectedGoPackageFiles(result *Result) ([]generatedFile, error) {
	files, err := cachedProjection(result, "go-packages", nil, func() ([]generatedFile, error) {
		return renderGoPackages(result)
	})
	if err != nil {
		return nil, err
	}
	// Locations and module ownership are live filesystem facts, never cache facts.
	if err := validateGoPackageLocations(result, files); err != nil {
		return nil, err
	}
	return files, nil
}
