package observability

import (
	"encoding/json"
	"errors"
)

// Catalog endpoints have different row shapes. Keep the original JSON kinds
// until the consuming endpoint validates every row, including omitted output.
func normalizeMetricCatalogSeries(items []json.RawMessage, limit int) ([]map[string]string, error) {
	if items == nil {
		return nil, errors.New("VictoriaMetrics series catalog requires an array")
	}
	capacity := len(items)
	if limit > 0 {
		capacity = min(capacity, limit)
	}
	out := make([]map[string]string, 0, capacity)
	for _, raw := range items {
		var labels map[string]any
		if err := json.Unmarshal(raw, &labels); err != nil || labels == nil {
			return nil, errors.New("VictoriaMetrics series catalog requires label objects")
		}
		emit := limit <= 0 || len(out) < limit
		var series map[string]string
		if emit {
			series = make(map[string]string, len(labels))
		}
		for name, rawValue := range labels {
			value, ok := rawValue.(string)
			if !ok {
				return nil, errors.New("VictoriaMetrics series catalog label requires a string")
			}
			if emit {
				series[name] = value
			}
		}
		if emit {
			out = append(out, series)
		}
	}
	return out, nil
}

func normalizeMetricCatalogLabels(items []json.RawMessage, limit int) ([]string, error) {
	if items == nil {
		return nil, errors.New("VictoriaMetrics labels catalog requires an array")
	}
	capacity := len(items)
	if limit > 0 {
		capacity = min(capacity, limit)
	}
	out := make([]string, 0, capacity)
	for _, raw := range items {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, errors.New("VictoriaMetrics labels catalog requires strings")
		}
		value, ok := decoded.(string)
		if !ok {
			return nil, errors.New("VictoriaMetrics labels catalog requires strings")
		}
		if limit <= 0 || len(out) < limit {
			out = append(out, value)
		}
	}
	return out, nil
}
