package service

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	onlava "github.com/pbrazdil/onlava"
)

var (
	cronMu    sync.Mutex
	cronState StatusResponse
)

type StatusResponse struct {
	Count int    `json:"count"`
	Cron  string `json:"cron"`
	Type  string `json:"type"`
	Path  string `json:"path"`
}

//onlava:api private
func Run(ctx context.Context) error {
	req := onlava.CurrentRequest()

	cronMu.Lock()
	defer cronMu.Unlock()
	state := readCronState()
	state.Count++
	state.Cron = req.CronIdempotencyKey
	state.Type = string(req.Type)
	state.Path = req.Path
	cronState = state
	return writeCronState(state)
}

func readCronState() StatusResponse {
	path := os.Getenv("ONLAVA_CRON_STATE_PATH")
	if path == "" {
		return cronState
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StatusResponse{}
	}
	var state StatusResponse
	if err := json.Unmarshal(data, &state); err != nil {
		return StatusResponse{}
	}
	return state
}

func writeCronState(state StatusResponse) error {
	path := os.Getenv("ONLAVA_CRON_STATE_PATH")
	if path == "" {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

//onlava:api public path=/cron/status method=GET
func Status(ctx context.Context) (*StatusResponse, error) {
	cronMu.Lock()
	defer cronMu.Unlock()
	resp := readCronState()
	return &resp, nil
}
