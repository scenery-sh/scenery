package uireport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanGolden(t *testing.T) {
	names := []string{"astryx-heavy.tsx", "catalog-and-lib.tsx", "raw-div.tsx", "svg-icon.jsx"}
	files := make([]SourceFile, 0, len(names))
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, SourceFile{Path: name, Content: content})
	}
	actual, err := json.MarshalIndent(Scan(files), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	expected, err := os.ReadFile(filepath.Join("testdata", "report.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("report mismatch\nactual:\n%s\nexpected:\n%s", actual, expected)
	}
}

func TestScanPinsLexicalTraps(t *testing.T) {
	source := SourceFile{Path: "page.tsx", Content: []byte(`
import * as stylex from "@stylexjs/stylex";
import { colorVars, spacingVars as space } from "./theme.stylex";
const styles = stylex.create({
  root: {
    color: colorVars["--color-text"],
    width: ` + "`calc(${space[\"--space\"]} * 2)`" + `,
    height: ` + "`${{ compact: space[\"--compact\"] }.compact}`" + `,
  },
});
// style={{ color: "#fff", width: "20px" }}
const text = "<div style={{ color: '#000' }} />";
export const Page = () => <div xstyle={styles.root} style={{ display: "block" }} />;
`)}
	report := Scan([]SourceFile{source})[0]
	if report.Style.TokenRefs != 3 {
		t.Fatalf("token refs = %d, want 3", report.Style.TokenRefs)
	}
	if report.Style.RawColors != 0 || report.Style.RawSizes != 0 {
		t.Fatalf("raw values = colors %d sizes %d, want zero", report.Style.RawColors, report.Style.RawSizes)
	}
	if report.Style.InlineStyleProps != 1 {
		t.Fatalf("inline styles = %d, want 1", report.Style.InlineStyleProps)
	}
	if report.Markup.Raw != 1 {
		t.Fatalf("raw tags = %d, want 1", report.Markup.Raw)
	}
}

func TestScanOmitsUndefinedShares(t *testing.T) {
	report := Scan([]SourceFile{{Path: "empty.tsx", Content: []byte("export const value = 1;\n")}})[0]
	if report.Markup.DSShare != nil || report.Style.TokenShare != nil {
		t.Fatalf("undefined shares = %#v %#v, want nil", report.Markup.DSShare, report.Style.TokenShare)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ds_share") || strings.Contains(string(encoded), "token_share") {
		t.Fatalf("undefined shares were serialized: %s", encoded)
	}
}
