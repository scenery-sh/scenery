package observability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type responseReadStep struct {
	data string
	err  error
}
type responseTestBody struct {
	steps  []responseReadStep
	closes int
}

func (b *responseTestBody) Read(p []byte) (int, error) {
	if len(b.steps) == 0 {
		return 0, io.EOF
	}
	step := &b.steps[0]
	n := copy(p, step.data)
	step.data = step.data[n:]
	if len(step.data) > 0 {
		return n, nil
	}
	err := step.err
	b.steps = b.steps[1:]
	return n, err
}
func (b *responseTestBody) Close() error { b.closes++; return nil }
func responseFixtureClient(body io.ReadCloser) *http.Client {
	return &http.Client{Transport: metricsQueryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: body}, nil
	})}
}
func finiteFixtureDocument(mode string) string {
	switch mode {
	case "vector":
		return `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1.000000001,"2"]}]}}`
	case "matrix":
		return `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1.000000001,"NaN"],[2,"+Inf"],[3,"-Inf"]]}]}}`
	case "labels":
		return `{"status":"success","data":["job"]}`
	default:
		return `{"status":"success","data":[{}]}`
	}
}
func callFiniteFixture(mode string, client *http.Client) (string, error) {
	if mode == "vector" || mode == "matrix" {
		r, err := QueryMetrics(context.Background(), MetricsQuery{BaseURL: "http://metrics.test", Scope: testScope(), Bounds: testBounds(), Instant: mode == "vector", client: client})
		return r.Kind, err
	}
	q := MetricsCatalogQuery{BaseURL: "http://metrics.test", Scope: testScope(), Bounds: testBounds(), client: client}
	if mode == "labels" {
		r, err := MetricsLabels(context.Background(), q)
		return r.Kind, err
	}
	r, err := MetricsSeries(context.Background(), q)
	return r.Kind, err
}

func TestMetricResponseFraming(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, suffix string
		valid        bool
	}{
		{"no newline", "", true}, {"ASCII whitespace", " \t\r\n", true},
		{"second object", ` {"private-response-token":1}`, false}, {"null", "null", false}, {"array", "[]", false}, {"garbage", "private-response-token", false},
		{"vertical tab", "\v", false}, {"form feed", "\f", false}, {"NBSP", "\u00a0", false}, {"em space", "\u2003", false},
	}
	for _, mode := range []string{"vector", "matrix", "labels", "series"} {
		for _, item := range cases {
			for _, later := range []bool{false, true} {
				t.Run(mode+"/"+item.name+map[bool]string{false: "/buffered", true: "/later"}[later], func(t *testing.T) {
					doc := finiteFixtureDocument(mode)
					steps := []responseReadStep{{doc + item.suffix, io.EOF}}
					if later {
						steps = []responseReadStep{{doc, nil}, {item.suffix, io.EOF}}
					}
					body := &responseTestBody{steps: steps}
					kind, err := callFiniteFixture(mode, responseFixtureClient(body))
					if (err == nil) != item.valid || (kind != "") != item.valid || body.closes != 1 {
						t.Fatalf("kind=%q err=%v closes=%d valid=%t", kind, err, body.closes, item.valid)
					}
					if err != nil && strings.Contains(err.Error(), "private-response-token") {
						t.Fatalf("response bytes exposed: %v", err)
					}
				})
			}
		}
	}
}

func TestMetricResponseReadErrors(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("scripted response read failure")
	for _, mode := range []string{"vector", "matrix", "labels", "series"} {
		doc := finiteFixtureDocument(mode)
		cases := []struct {
			name  string
			steps []responseReadStep
			want  error
		}{
			{"same read", []responseReadStep{{doc, sentinel}}, sentinel},
			{"later read", []responseReadStep{{doc, nil}, {"", sentinel}}, sentinel},
			{"whitespace and read failure", []responseReadStep{{doc, nil}, {" \t", sentinel}}, sentinel},
			{"short document", []responseReadStep{{doc[:len(doc)-1], io.ErrUnexpectedEOF}}, io.ErrUnexpectedEOF},
			{"clean bytes and EOF", []responseReadStep{{doc, io.EOF}}, nil},
			{"split terminal whitespace", []responseReadStep{{doc, nil}, {" ", nil}, {"\t\r", nil}, {"\n", io.EOF}}, nil},
		}
		for _, item := range cases {
			t.Run(mode+"/"+item.name, func(t *testing.T) {
				body := &responseTestBody{steps: item.steps}
				kind, err := callFiniteFixture(mode, responseFixtureClient(body))
				if !errors.Is(err, item.want) || body.closes != 1 || (kind != "") != (item.want == nil) {
					t.Fatalf("kind=%q err=%v want=%v closes=%d", kind, err, item.want, body.closes)
				}
			})
		}
	}
}

// Exact log numbers must survive complete-line validation without float64.
func assertLogNumber(t *testing.T, entry LogEntry) {
	t.Helper()
	number, ok := entry.Raw["n"].(json.Number)
	if !ok || number.String() != "9007199254740993" {
		t.Fatalf("log number changed: %#v", entry.Raw)
	}
}

func TestLogResponseFraming(t *testing.T) {
	t.Parallel()
	const row = `{"_msg":"valid","n":9007199254740993}`
	cases := []struct{ name, bad string }{
		{"second object", row + ` {"private-response-token":1}`}, {"null suffix", row + ` null`}, {"array suffix", row + ` []`}, {"garbage suffix", row + `private-response-token`},
		{"leading vertical tab", "\v" + row}, {"trailing form feed", row + "\f"}, {"leading NBSP", "\u00a0" + row}, {"trailing em space", row + "\u2003"}, {"Unicode blank", "\u2003"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			input := row + "\n" + item.bad + "\n" + row + "\n"
			for _, tail := range []bool{false, true} {
				body := &responseTestBody{steps: []responseReadStep{{input, io.EOF}}}
				client := responseFixtureClient(body)
				q := LogsQuery{BaseURL: "http://logs.test", Scope: testScope(), Bounds: testBounds()}
				var err error
				if tail {
					count := 0
					err = tailLogsWithClient(context.Background(), client, q, func(e LogsTailEntry) error { count++; assertLogNumber(t, e.Log); return nil })
					if count != 1 {
						t.Fatalf("tail emitted %d rows instead of valid prefix", count)
					}
				} else {
					result, callErr := queryLogsWithClient(context.Background(), client, q)
					err = callErr
					if result.Kind != "" || len(result.Logs) != 0 {
						t.Fatalf("malformed input became successful envelope: %+v", result)
					}
				}
				if err == nil || strings.Contains(err.Error(), "private-response-token") || body.closes != 1 {
					t.Fatalf("err=%v closes=%d", err, body.closes)
				}
			}
		})
	}
	for _, input := range []string{row, " \t\r\n\n\t " + row + " \t\r\n" + row} {
		body := &responseTestBody{steps: []responseReadStep{{input, io.EOF}}}
		result, err := queryLogsWithClient(context.Background(), responseFixtureClient(body), LogsQuery{BaseURL: "http://logs.test"})
		want := 1
		if strings.Contains(input, "\n") {
			want = 2
		}
		if err != nil || len(result.Logs) != want || body.closes != 1 {
			t.Fatalf("valid lines failed: %+v err=%v closes=%d", result, err, body.closes)
		}
		for _, entry := range result.Logs {
			assertLogNumber(t, entry)
		}
	}
}

func TestLogResponseLifecycle(t *testing.T) {
	t.Parallel()
	const row = `{"_msg":"valid","n":9007199254740993}` + "\n"
	sentinel := errors.New("scripted log read failure")
	for _, tail := range []bool{false, true} {
		body := &responseTestBody{steps: []responseReadStep{{row, nil}, {"", sentinel}}}
		client := responseFixtureClient(body)
		q := LogsQuery{BaseURL: "http://logs.test"}
		var err error
		if tail {
			count := 0
			err = tailLogsWithClient(context.Background(), client, q, func(e LogsTailEntry) error { count++; assertLogNumber(t, e.Log); return nil })
			if count != 1 {
				t.Fatalf("prefix count=%d", count)
			}
		} else {
			result, callErr := queryLogsWithClient(context.Background(), client, q)
			err = callErr
			if result.Kind != "" || len(result.Logs) != 0 {
				t.Fatalf("read error became success: %+v", result)
			}
		}
		if !errors.Is(err, sentinel) || body.closes != 1 {
			t.Fatalf("err=%v closes=%d", err, body.closes)
		}
	}
	body := &responseTestBody{steps: []responseReadStep{{row + row, io.EOF}}}
	count := 0
	err := tailLogsWithClient(context.Background(), responseFixtureClient(body), LogsQuery{BaseURL: "http://logs.test"}, func(LogsTailEntry) error { count++; return sentinel })
	if !errors.Is(err, sentinel) || count != 1 || body.closes != 1 {
		t.Fatalf("callback err=%v count=%d closes=%d", err, count, body.closes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: metricsQueryTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	err = tailLogsWithClient(ctx, client, LogsQuery{BaseURL: "http://logs.test"}, func(LogsTailEntry) error { t.Fatal("callback after canceled request"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
}
