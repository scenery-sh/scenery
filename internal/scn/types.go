package scn

type Position struct {
	Line       int `json:"line"`
	Column     int `json:"column"`
	ByteOffset int `json:"byte_offset"`
}

type Range struct {
	SourceID string   `json:"source_id"`
	Start    Position `json:"start"`
	End      Position `json:"end"`
}

// Diagnostic is a syntax/source diagnostic emitted before graph compilation.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	// Path is the root-relative source file the diagnostic points at. Range
	// only carries the hashed SourceID, which consumers cannot reverse, so
	// the readable path must travel with the diagnostic itself.
	Path  string `json:"path,omitempty"`
	Range *Range `json:"range,omitempty"`
}
