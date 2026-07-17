package uireport

import (
	"sort"
)

// SourceFile is hand-authored React source supplied to the pure scanner.
type SourceFile struct {
	Path    string
	Content []byte
}

// Report is the command-independent UI adherence payload.
type Report struct {
	Frontends []FrontendReport `json:"frontends"`
}

type FrontendReport struct {
	Name         string         `json:"name"`
	Root         string         `json:"root"`
	DesignSystem string         `json:"design_system"`
	Files        []FileReport   `json:"files"`
	Totals       FrontendTotals `json:"totals"`
}

type FileReport struct {
	Path   string       `json:"path"`
	Lines  int          `json:"lines"`
	Markup MarkupReport `json:"markup"`
	Style  StyleReport  `json:"style"`
	Score  int          `json:"score"`

	designSystemImports int
}

type MarkupReport struct {
	DesignSystem int      `json:"design_system"`
	Catalog      int      `json:"catalog"`
	Raw          int      `json:"raw"`
	Local        int      `json:"local"`
	Lib          int      `json:"lib"`
	SVG          int      `json:"svg"`
	DSShare      *float64 `json:"ds_share,omitempty"`
}

type StyleReport struct {
	TokenRefs        int      `json:"token_refs"`
	RawColors        int      `json:"raw_colors"`
	RawSizes         int      `json:"raw_sizes"`
	InlineStyleProps int      `json:"inline_style_props"`
	TokenShare       *float64 `json:"token_share,omitempty"`
}

type FrontendTotals struct {
	Files  int          `json:"files"`
	Lines  int          `json:"lines"`
	Markup MarkupReport `json:"markup"`
	Style  StyleReport  `json:"style"`
	Score  int          `json:"score"`
}

// The weights intentionally rank structural HTML debt first while making
// colors and inline styles more visible. They are the v1 contract recorded in
// ExecPlan 0124; enforcement is deliberately not derived from this score.
const (
	rawMarkupWeight   = 1
	rawColorWeight    = 2
	rawSizeWeight     = 1
	inlineStyleWeight = 3
)

func finalizeFile(report *FileReport) {
	report.Markup.DSShare = markupShare(report.Markup)
	report.Style.TokenShare = tokenShare(report.Style)
	report.Score = report.Markup.Raw*rawMarkupWeight +
		report.Style.RawColors*rawColorWeight +
		report.Style.RawSizes*rawSizeWeight +
		report.Style.InlineStyleProps*inlineStyleWeight
}

func aggregate(files []FileReport) FrontendTotals {
	totals := FrontendTotals{Files: len(files)}
	for _, file := range files {
		totals.Lines += file.Lines
		totals.Markup.DesignSystem += file.Markup.DesignSystem
		totals.Markup.Catalog += file.Markup.Catalog
		totals.Markup.Raw += file.Markup.Raw
		totals.Markup.Local += file.Markup.Local
		totals.Markup.Lib += file.Markup.Lib
		totals.Markup.SVG += file.Markup.SVG
		totals.Style.TokenRefs += file.Style.TokenRefs
		totals.Style.RawColors += file.Style.RawColors
		totals.Style.RawSizes += file.Style.RawSizes
		totals.Style.InlineStyleProps += file.Style.InlineStyleProps
		totals.Score += file.Score
	}
	totals.Markup.DSShare = markupShare(totals.Markup)
	totals.Style.TokenShare = tokenShare(totals.Style)
	return totals
}

func markupShare(markup MarkupReport) *float64 {
	denominator := markup.DesignSystem + markup.Catalog + markup.Raw + markup.Local + markup.Lib
	if denominator == 0 {
		return nil
	}
	value := float64(markup.DesignSystem+markup.Catalog) / float64(denominator)
	return &value
}

func tokenShare(style StyleReport) *float64 {
	denominator := style.TokenRefs + style.RawColors + style.RawSizes
	if denominator == 0 {
		return nil
	}
	value := float64(style.TokenRefs) / float64(denominator)
	return &value
}

func sortFiles(files []FileReport) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].Score != files[j].Score {
			return files[i].Score > files[j].Score
		}
		return files[i].Path < files[j].Path
	})
}
