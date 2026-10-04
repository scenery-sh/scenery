package generate

func renderTSRuntimePublic() string {
	return renderTSRuntimeTypes() + tsRuntimeJSON + tsRuntimeHTTP + tsRuntimeEncoding
}
