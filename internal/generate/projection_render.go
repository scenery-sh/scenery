package generate

// Private composition embeds implementation and workspace identity, unlike
// app-imported contracts. Cache only its pure bytes, never retirement checks,
// overlay construction, native verification or publication ownership.
func renderExpectedGoApplicationFiles(result *Result, input projectionInput) ([]generatedFile, error) {
	identity := struct {
		WorkspaceRevision       string
		ImplementationRevisions map[string]string
	}{result.WorkspaceRevision, result.ImplementationRevisions}
	return cachedProjection(input, "go-application", identity, func() ([]generatedFile, error) {
		return generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), input)
	})
}

// renderExpectedGoPackageFiles is the shared app-imported projection. Build
// workspaces add private adapters/composition to these exact package bytes.
func renderExpectedGoPackageFiles(result *Result) ([]generatedFile, error) {
	return renderGoPackageProjection(result, newProjectionInput(result))
}

func renderGoPackageProjection(result *Result, input projectionInput) ([]generatedFile, error) {
	files, err := cachedProjection(input, "go-packages", nil, func() ([]generatedFile, error) {
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
