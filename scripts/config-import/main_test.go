package main

import (
	"strings"
	"testing"
)

func TestParseDotenvReportsWhatItCannotConvertWithoutGuessing(t *testing.T) {
	entries, err := parseDotenv([]byte("\xef\xbb\xbf# comment\nexport JWT_SECRET='s3cret $HOME'\nGOOGLE_OAUTH_CLIENT_ID=\"client\\tid\"\nPACK=$HOME/packs\nNOTE=value # trailing\nTWICE=a\nTWICE=b\nPLAIN=value=with=equals\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]entry{}
	for _, current := range entries {
		got[current.name] = current
	}
	if got["JWT_SECRET"].value != "s3cret $HOME" || got["JWT_SECRET"].problem != "" {
		t.Fatalf("single-quoted = %+v", got["JWT_SECRET"])
	}
	if got["GOOGLE_OAUTH_CLIENT_ID"].value != "client\tid" || got["GOOGLE_OAUTH_CLIENT_ID"].problem != "" {
		t.Fatalf("double-quoted = %+v", got["GOOGLE_OAUTH_CLIENT_ID"])
	}
	for _, name := range []string{"PACK", "NOTE", "TWICE"} {
		if got[name].problem == "" {
			t.Fatalf("%s converted without a problem: %+v", name, got[name])
		}
	}
	if got["PLAIN"].value != "value=with=equals" {
		t.Fatalf("plain = %+v", got["PLAIN"])
	}
	if _, err := parseDotenv([]byte("not a pair\n")); err == nil {
		t.Fatal("malformed line accepted")
	}
}

func TestPlanWritesBlocksUnsafeConversionsAndHidesValues(t *testing.T) {
	entries := []entry{{name: "JWT_SECRET", value: "hunter2"}, {name: "PACK", value: "/p", problem: "uses $ expansion; set this key manually"}, {name: "UNUSED", value: "x"}, {name: "EXISTING", value: "1"}}
	catalog := map[string]catalogInput{
		"auth.jwt_secret":           {Key: "auth.jwt_secret", Type: `resource_ref("secret")`, Sensitive: true, Source: "none"},
		"designs.weather_pack_root": {Key: "designs.weather_pack_root", Type: "optional(host_path)", Source: "none"},
		"designs.limit":             {Key: "designs.limit", Type: "uint32", Source: "environment"},
	}
	plan, blocked := planWrites(entries, catalog, map[string]string{"PACK": "designs.weather_pack_root", "EXISTING": "designs.limit"}, false)
	if !blocked {
		t.Fatal("an expansion was not blocking")
	}
	status := map[string]string{}
	for _, write := range plan {
		status[write.name] = write.status
	}
	if status["JWT_SECRET"] != "ready" || !strings.Contains(status["PACK"], "expansion") || !strings.HasPrefix(status["UNUSED"], "unmapped") || !strings.HasPrefix(status["EXISTING"], "already configured") {
		t.Fatalf("plan = %v", status)
	}
	var rendered strings.Builder
	renderPlan(&rendered, plan)
	if strings.Contains(rendered.String(), "hunter2") {
		t.Fatal("preview shows a value")
	}
}
