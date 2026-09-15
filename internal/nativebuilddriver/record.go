package nativebuilddriver

import "time"

const ProtocolVersion = "scenery.native-build-driver"

const ProtocolRevision = 1

type ToolRecord struct {
	Protocol   string           `json:"protocol"`
	ID         string           `json:"id"`
	Tool       string           `json:"tool"`
	Argv       []string         `json:"argv"`
	CWD        string           `json:"cwd"`
	StartedAt  time.Time        `json:"started_at"`
	DurationMS float64          `json:"duration_ms"`
	ExitCode   int              `json:"exit_code"`
	Files      map[int]FileCopy `json:"files,omitempty"`
	Output     *FileCopy        `json:"output,omitempty"`
}

type FileCopy struct {
	Original string `json:"original"`
	Copy     string `json:"copy"`
	Digest   string `json:"digest"`
	Bytes    int64  `json:"bytes"`
}
