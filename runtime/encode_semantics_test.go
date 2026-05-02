package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type EncodeEmbedded struct {
	Embedded string `json:"embedded"`
}

type encodeResponse struct {
	EncodeEmbedded
	Name   string `json:"name"`
	Hidden string `json:"-"`
	Empty  string `json:"empty,omitempty"`
	Header string `header:"X-Onlava-Test"`
	Status int    `onlava:"httpstatus"`
}

func TestEncodeResponseHonorsJSONTagsWhenShapingResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	resp := encodeResponse{
		EncodeEmbedded: EncodeEmbedded{Embedded: "yes"},
		Name:           "onlava",
		Hidden:         "secret",
		Header:         "header-value",
		Status:         http.StatusCreated,
	}
	if err := encodeResponseWithStatus(rec, resp, 0); err != nil {
		t.Fatalf("encodeResponseWithStatus() error = %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("X-Onlava-Test"); got != "header-value" {
		t.Fatalf("header = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\n%s", err, rec.Body.String())
	}
	if body["name"] != "onlava" || body["embedded"] != "yes" {
		t.Fatalf("body = %#v", body)
	}
	if _, ok := body["-"]; ok {
		t.Fatalf("body included json dash field: %#v", body)
	}
	if _, ok := body["Hidden"]; ok {
		t.Fatalf("body included hidden field: %#v", body)
	}
	if _, ok := body["empty"]; ok {
		t.Fatalf("body included omitempty field: %#v", body)
	}
}

type customJSONResponse struct{}

func (customJSONResponse) MarshalJSON() ([]byte, error) {
	return []byte(`{"custom":true}`), nil
}

func TestEncodeResponseHonorsCustomMarshalerWithoutShapeTags(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := encodeResponseWithStatus(rec, customJSONResponse{}, 0); err != nil {
		t.Fatalf("encodeResponseWithStatus() error = %v", err)
	}
	if got := rec.Body.String(); got != "{\"custom\":true}\n" {
		t.Fatalf("body = %q", got)
	}
}
