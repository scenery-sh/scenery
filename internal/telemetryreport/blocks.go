package telemetryreport

import (
	"encoding/json"
	"sort"
	"time"
)

// BuildBlock describes one observed block. Without ended_at, a historical
// duration is a lower bound ending at last_seen; live blocks end at window.until.
type BuildBlock struct {
	AppRoot         string `json:"app_root"`
	Reason          string `json:"reason"`
	Cause           string `json:"cause"`
	Since           string `json:"since"`
	LastSeen        string `json:"last_seen"`
	EndedAt         string `json:"ended_at,omitempty"`
	DurationMS      int64  `json:"duration_ms"`
	PreventedBuilds int    `json:"prevented_builds"`
}

type blockEvent struct {
	Reason          string    `json:"reason"`
	Cause           string    `json:"cause"`
	Since           time.Time `json:"since"`
	PreventedBuilds int       `json:"prevented_builds"`
}

func recordBlockEvent(blocks map[string]*BuildBlock, root, kind string, at time.Time, raw json.RawMessage) {
	var event blockEvent
	if json.Unmarshal(raw, &event) != nil || event.Since.IsZero() || event.Reason == "" {
		return
	}
	key := root + "\x00" + event.Since.UTC().Format(time.RFC3339Nano)
	block := blocks[key]
	if block == nil {
		block = &BuildBlock{AppRoot: root, Reason: event.Reason, Cause: failureCause(event.Cause, ""), Since: event.Since.UTC().Format(time.RFC3339Nano)}
		blocks[key] = block
	}
	block.LastSeen = at.UTC().Format(time.RFC3339Nano)
	block.DurationMS = max(0, at.Sub(event.Since).Milliseconds())
	block.PreventedBuilds = max(block.PreventedBuilds, event.PreventedBuilds)
	if kind == "build.unblocked" {
		block.EndedAt = block.LastSeen
	}
}

func finalizeBlocks(builds *Builds, blocks map[string]*BuildBlock, opts Options) {
	for _, block := range blocks {
		builds.Blocks = append(builds.Blocks, *block)
		builds.PreventedBuilds += block.PreventedBuilds
	}
	for _, block := range opts.ActiveBlocks {
		block.Cause = failureCause(block.Cause, "")
		block.LastSeen = opts.Until.UTC().Format(time.RFC3339Nano)
		if since, err := time.Parse(time.RFC3339Nano, block.Since); err == nil {
			block.DurationMS = max(0, opts.Until.Sub(since).Milliseconds())
		}
		builds.ActiveBlocks = append(builds.ActiveBlocks, block)
	}
	for _, values := range [][]BuildBlock{builds.Blocks, builds.ActiveBlocks} {
		sort.Slice(values, func(i, j int) bool {
			if values[i].DurationMS != values[j].DurationMS {
				return values[i].DurationMS > values[j].DurationMS
			}
			return values[i].AppRoot < values[j].AppRoot
		})
	}
}
