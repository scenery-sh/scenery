package observability

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/schemacheck"
)

type metricsQueryTransport func(*http.Request) (*http.Response, error)

func (f metricsQueryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The public query boundary must reject all malformed samples, even after the
// requested series limit, instead of returning apparently complete evidence.
func TestMetricsQueryRejectsMalformedSamples(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"mixed matrix":            `{"resultType":"matrix","result":[{"metric":{},"values":[[1812450000,"3"],["not-a-time","4"]]}]}`,
		"null item":               `{"resultType":"vector","result":[null]}`,
		"missing metric":          `{"resultType":"vector","result":[{"value":[1812450000,"3"]}]}`,
		"null metric":             `{"resultType":"vector","result":[{"metric":null,"value":[1812450000,"3"]}]}`,
		"short tuple":             `{"resultType":"vector","result":[{"metric":{},"value":[1812450000]}]}`,
		"long tuple":              `{"resultType":"vector","result":[{"metric":{},"value":[1812450000,"3","extra"]}]}`,
		"null tuple":              `{"resultType":"matrix","result":[{"metric":{},"values":[null]}]}`,
		"string timestamp":        `{"resultType":"vector","result":[{"metric":{},"value":["1812450000","3"]}]}`,
		"infinite timestamp":      `{"resultType":"vector","result":[{"metric":{},"value":["+Inf","3"]}]}`,
		"out of range timestamp":  `{"resultType":"vector","result":[{"metric":{},"value":[1e30,"3"]}]}`,
		"numeric value":           `{"resultType":"vector","result":[{"metric":{},"value":[1812450000,3]}]}`,
		"invalid value":           `{"resultType":"vector","result":[{"metric":{},"value":[1812450000,"invalid"]}]}`,
		"null value":              `{"resultType":"vector","result":[{"metric":{},"value":[1812450000,null]}]}`,
		"missing vector value":    `{"resultType":"vector","result":[{"metric":{}}]}`,
		"wrong vector shape":      `{"resultType":"vector","result":[{"metric":{},"values":[[1812450000,"3"]]}]}`,
		"null matrix values":      `{"resultType":"matrix","result":[{"metric":{},"values":null}]}`,
		"wrong matrix shape":      `{"resultType":"matrix","result":[{"metric":{},"value":[1812450000,"3"],"values":[]}]}`,
		"null result":             `{"resultType":"vector","result":null}`,
		"missing result":          `{"resultType":"vector"}`,
		"missing result type":     `{"result":[]}`,
		"unsupported result type": `{"resultType":"scalar","result":[]}`,
		"malformed after limit":   `{"resultType":"vector","result":[{"metric":{},"value":[1812450000,"3"]},{"metric":{},"value":[1812450000]}]}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := QueryMetrics(context.Background(), metricsFixtureQuery(data, true))
			if err == nil || len(result.Series) != 0 {
				t.Fatalf("malformed response became partial success: %+v err=%v", result, err)
			}
		})
	}
	q := metricsFixtureQuery(`{"resultType":"vector","result":[]}`, false)
	if _, err := QueryMetrics(context.Background(), q); err == nil {
		t.Fatal("range endpoint accepted vector result")
	}
}

func TestMetricsQueryPreservesValidSamples(t *testing.T) {
	t.Parallel()
	cases := []struct {
		data    string
		instant bool
		samples int
		value   string
	}{
		{`{"resultType":"vector","result":[{"metric":{"__name__":"x"},"value":[1812450000.125,"3"]}]}`, true, 1, "3"},
		{`{"resultType":"matrix","result":[{"metric":{},"values":[[1812450000,"NaN"],[1812450001,"+Inf"],[1812450002,"-Inf"]]}]}`, false, 3, ""},
		{`{"resultType":"matrix","result":[{"metric":{},"values":[]}]}`, true, 0, ""},
		{`{"resultType":"vector","result":[]}`, true, 0, ""},
		{`{"resultType":"matrix","result":[]}`, false, 0, ""},
	}
	for _, fixture := range cases {
		t.Run(fixture.data, func(t *testing.T) {
			result, err := QueryMetrics(context.Background(), metricsFixtureQuery(fixture.data, fixture.instant))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, series := range result.Series {
				count += len(series.Values)
				if series.Value != nil {
					count++
					if series.Value.Value != fixture.value || series.Value.Time != time.Unix(1812450000, 125000000).UTC().Format(time.RFC3339Nano) {
						t.Fatalf("vector value changed: %+v", series.Value)
					}
				}
			}
			if count != fixture.samples {
				t.Fatalf("sample count=%d want=%d", count, fixture.samples)
			}
			if diagnostics := schemacheck.ValidateFile(filepath.Join("..", "..", "docs", "schemas", "scenery.metrics.query.schema.json"), result); len(diagnostics) > 0 {
				t.Fatalf("schema diagnostics: %v", diagnostics)
			}
		})
	}
}

func metricsFixtureQuery(data string, instant bool) MetricsQuery {
	at := time.Unix(1812450000, 0).UTC()
	return MetricsQuery{BaseURL: "http://metrics.test", PromQL: "x", Bounds: TimeBounds{Start: at.Add(-time.Minute), End: at}, Step: time.Second, Instant: instant, Limit: 1, client: &http.Client{Transport: metricsQueryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":"success","data":` + data + `}`)), Header: make(http.Header), Request: r}, nil
	})}}
}
