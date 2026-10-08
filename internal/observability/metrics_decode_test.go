package observability

import (
	"encoding/json"
	"testing"
)

func TestMetricsResponseRejectsMalformedResults(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		`{"resultType":"vector","result":[42]}`,
		`{"resultType":"vector","result":[{"metric":42}]}`,
		`{"resultType":"matrix","result":[{"values":"invalid"}]}`,
	} {
		var response victoriaMetricsResponse
		if err := json.Unmarshal([]byte(`{"status":"success","data":`+data+`}`), &response); err == nil {
			t.Fatalf("malformed backend result accepted: %s", data)
		}
	}
	for _, data := range []string{
		`{"resultType":"vector","result":[{"metric":{"__name__":"requests"},"value":[1812450000,"3"]}]}`,
		`{"resultType":"matrix","result":[{"metric":{"__name__":"requests"},"values":[[1812450000,"3"]]}]}`,
		`{"resultType":"vector","result":[]}`,
		`["job","instance"]`,
		`[{"job":"api"}]`,
	} {
		var response victoriaMetricsResponse
		if err := json.Unmarshal([]byte(`{"status":"success","data":`+data+`}`), &response); err != nil {
			t.Fatalf("valid backend result rejected: %s: %v", data, err)
		}
	}
}
