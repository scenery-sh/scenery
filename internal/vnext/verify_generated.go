package vnext

func Check(root string) (*Result, error) {
	return check(root, false)
}

func check(root string, allowActiveChangeTransaction bool) (*Result, error) {
	result, err := compile(root, allowActiveChangeTransaction)
	if err != nil || !result.Valid() {
		return result, err
	}
	if _, generateErr := generateGoContractsFromResult(result, true); generateErr != nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "SCN6203", Severity: "error", Message: generateErr.Error()})
		result.ImplementationStatus = "invalid"
	}
	if _, generateErr := generateTypeScriptClientsFromResult(result, "", true); generateErr != nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "SCN6204", Severity: "error", Message: generateErr.Error()})
		result.ImplementationStatus = "invalid"
	}
	if result.Manifest != nil {
		result.Manifest.Diagnostics = append([]Diagnostic(nil), result.Diagnostics...)
	}
	return result, nil
}
