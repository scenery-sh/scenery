package runtime

import (
	"encoding/json"
)

type processLinkResponse struct {
	Output json.RawMessage   `json:"output,omitempty"`
	Error  *processLinkError `json:"error,omitempty"`
}
