package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/schemacheck"
)

func TestMetricsSeriesRejectsMalformedCatalog(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"null label":           `[{"private-label":null}]`,
		"mixed labels":         `[{"job":"api","private-label":null}]`,
		"null beyond limit":    `[{"job":"api"},{"private-label":null}]`,
		"null row":             `[null]`,
		"null row after limit": `[{"job":"api"},null]`,
		"string row":           `["private-row"]`,
		"string after limit":   `[{"job":"api"},"private-row"]`,
		"numeric label":        `[{"private-label":7}]`,
		"boolean label":        `[{"private-label":false}]`,
		"object label":         `[{"private-label":{}}]`,
		"array label":          `[{"private-label":[]}]`,
		"null data":            `null`,
		"object data":          `{}`,
		"query data":           `{"resultType":"vector","result":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := MetricsSeries(context.Background(), metricCatalogFixture(data, 1))
			if err == nil || len(result.Series) != 0 || result.Kind != "" {
				t.Fatalf("malformed catalog became success: %+v err=%v", result, err)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatalf("catalog error exposed payload or selector: %v", err)
			}
		})
	}
}

func TestMetricsSeriesPreservesCatalog(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		data  string
		limit int
		want  []map[string]string
	}{
		{`[]`, 1, []map[string]string{}},
		{`[{}]`, 1, []map[string]string{{}}},
		{`[{"job":""}]`, 1, []map[string]string{{"job": ""}}},
		{`[{"job":" null \t"}]`, 1, []map[string]string{{"job": " null \t"}}},
		{`[{"job":"č/\\\"","__name__":"request_seconds"}]`, 1, []map[string]string{{"job": "č/\\\"", "__name__": "request_seconds"}}},
		{`[{"job":"api"},{"job":"worker"}]`, 1, []map[string]string{{"job": "api"}}},
		{`[{"job":"api"},{"job":"worker"}]`, 0, []map[string]string{{"job": "api"}, {"job": "worker"}}},
	} {
		t.Run(fixture.data, func(t *testing.T) {
			result, err := MetricsSeries(context.Background(), metricCatalogFixture(fixture.data, fixture.limit))
			if err != nil || !reflect.DeepEqual(result.Series, fixture.want) {
				t.Fatalf("catalog changed: %+v err=%v want=%+v", result, err, fixture.want)
			}
			if diagnostics := schemacheck.ValidateFile(filepath.Join("..", "..", "docs", "schemas", "scenery.metrics.series.schema.json"), result); len(diagnostics) > 0 {
				t.Fatalf("schema diagnostics: %v", diagnostics)
			}
		})
	}
	result, err := MetricsSeries(context.Background(), MetricsCatalogQuery{})
	if err != nil || result.Series == nil || len(result.Series) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("unavailable backend behavior changed: %+v err=%v", result, err)
	}
}

func TestMetricsLabelsRejectsMalformedCatalog(t *testing.T) {
	t.Parallel()
	for _, data := range []string{`null`, `[null]`, `["job",null]`, `[{"job":"api"}]`, `["job",{}]`, `[7]`, `[false]`, `[["job"]]`, `{"resultType":"vector","result":[]}`} {
		t.Run(data, func(t *testing.T) {
			result, err := MetricsLabels(context.Background(), metricCatalogFixture(data, 1))
			if err == nil || len(result.Labels) != 0 || result.Kind != "" {
				t.Fatalf("wrong labels catalog became success: %+v err=%v", result, err)
			}
		})
	}
}

func TestMetricsLabelsPreservesCatalog(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		data  string
		limit int
		want  []string
	}{
		{`[]`, 1, []string{}},
		{`[""]`, 1, []string{""}},
		{`["z","a"]`, 1, []string{"z"}},
		{`["z","a"]`, 0, []string{"a", "z"}},
	} {
		t.Run(fixture.data, func(t *testing.T) {
			result, err := MetricsLabels(context.Background(), metricCatalogFixture(fixture.data, fixture.limit))
			if err != nil || !reflect.DeepEqual(result.Labels, fixture.want) {
				t.Fatalf("labels changed: %+v err=%v want=%+v", result, err, fixture.want)
			}
			if diagnostics := schemacheck.ValidateFile(filepath.Join("..", "..", "docs", "schemas", "scenery.metrics.labels.schema.json"), result); len(diagnostics) > 0 {
				t.Fatalf("schema diagnostics: %v", diagnostics)
			}
		})
	}
}

func metricCatalogFixture(data string, limit int) MetricsCatalogQuery {
	query := metricsFixtureQuery(data, true)
	return MetricsCatalogQuery{BaseURL: query.BaseURL, Bounds: query.Bounds, Scope: testScope(), Match: "private-selector", Limit: limit, client: query.client}
}

type metricCatalogBody struct {
	io.Reader
	closed *bool
}

func (b metricCatalogBody) Close() error { *b.closed = true; return nil }

func TestMetricsCatalogRequestLifecycle(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"series", "labels"} {
		t.Run(endpoint, func(t *testing.T) {
			valid := `[{"job":"api"}]`
			if endpoint == "labels" {
				valid = `["job"]`
			}
			for _, body := range []string{`{"status":"success","data":` + valid + `}`, `{"status":"success","data":null}`, `{"status":"success"}`, `{"status":"success","data":` + valid + `}`} {
				closed := false
				q := MetricsCatalogQuery{BaseURL: "http://metrics.test", Scope: testScope(), Bounds: testBounds(), Match: "private-selector", Limit: 1, Timeout: time.Second}
				q.client = &http.Client{Transport: metricsQueryTransport(func(r *http.Request) (*http.Response, error) {
					if err := r.Context().Err(); err != nil {
						return nil, err
					}
					if r.Method != http.MethodPost || r.URL.Path != "/prometheus/api/v1/"+endpoint || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
						t.Fatalf("catalog request changed: %s %s %+v", r.Method, r.URL.Path, r.Header)
					}
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if r.Form.Get("match[]") != q.Match || r.Form.Get("start") != q.Bounds.Start.Format(time.RFC3339Nano) || r.Form.Get("end") != q.Bounds.End.Format(time.RFC3339Nano) || !reflect.DeepEqual(r.Form["extra_label"], []string{"scenery_app=demo", "scenery_session_id=session-a", "scenery_app_root_hash=root123"}) {
						t.Fatalf("catalog scope or bounds changed: %+v", r.Form)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: metricCatalogBody{Reader: strings.NewReader(body), closed: &closed}}, nil
				})}
				call := func(ctx context.Context) (int, error) {
					if endpoint == "series" {
						result, err := MetricsSeries(ctx, q)
						return len(result.Series), err
					}
					result, err := MetricsLabels(ctx, q)
					return len(result.Labels), err
				}
				count, err := call(context.Background())
				wantSuccess := strings.Contains(body, valid)
				if (err == nil) != wantSuccess || !closed || (wantSuccess && count != 1) || (!wantSuccess && count != 0) {
					t.Fatalf("catalog lifecycle: count=%d err=%v closed=%t body=%s", count, err, closed, body)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, err := call(ctx); !errors.Is(err, context.Canceled) {
					t.Fatalf("catalog cancellation lost: %v", err)
				}
			}
		})
	}
}
