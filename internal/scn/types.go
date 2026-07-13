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
	Range    *Range `json:"range,omitempty"`
}
