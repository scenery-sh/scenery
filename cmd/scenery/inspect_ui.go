package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	appcfg "scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/uireport"
)

const inspectUIKind = "scenery.inspect.ui"

type inspectUIOptions struct {
	Frontend string
}

type inspectUIResponse struct {
	cliPayloadIdentity
	App       inspectdata.AppRef        `json:"app"`
	Frontends []uireport.FrontendReport `json:"frontends"`
}

func buildInspectUIResponse(appRoot string, cfg appcfg.Config, frontend string) (inspectUIResponse, error) {
	report, err := uireport.Collect(appRoot, cfg, strings.TrimSpace(frontend))
	if err != nil {
		return inspectUIResponse{}, err
	}
	return inspectUIResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(inspectUIKind),
		App:                inspectAppInfo(appRoot, cfg, nil),
		Frontends:          report.Frontends,
	}, nil
}

func writeInspectUIHuman(output io.Writer, response inspectUIResponse) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	for index, frontend := range response.Frontends {
		if index > 0 {
			_, _ = fmt.Fprintln(writer)
		}
		_, _ = fmt.Fprintf(writer, "Frontend: %s (%s)  design system: %s\n", frontend.Name, frontend.Root, frontend.DesignSystem)
		if frontend.DesignSystem == "none" {
			_, _ = fmt.Fprintln(writer, "No Astryx, @scenery/ui, or StyleX token imports found.")
			continue
		}
		_, _ = fmt.Fprintln(writer, "SCORE\tLINES\tDS\tCAT\tRAW\tLOCAL\tLIB\tSVG\tDS%\tTOKENS\tCOLORS\tSIZES\tTOK%\tINLINE\tFILE")
		for _, file := range frontend.Files {
			_, _ = fmt.Fprintf(
				writer,
				"%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%d\t%d\t%d\t%s\t%d\t%s\n",
				file.Score,
				file.Lines,
				file.Markup.DesignSystem,
				file.Markup.Catalog,
				file.Markup.Raw,
				file.Markup.Local,
				file.Markup.Lib,
				file.Markup.SVG,
				formatInspectUIShare(file.Markup.DSShare),
				file.Style.TokenRefs,
				file.Style.RawColors,
				file.Style.RawSizes,
				formatInspectUIShare(file.Style.TokenShare),
				file.Style.InlineStyleProps,
				file.Path,
			)
		}
		totals := frontend.Totals
		_, _ = fmt.Fprintf(
			writer,
			"%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%d\t%d\t%d\t%s\t%d\tTOTAL (%d files)\n",
			totals.Score,
			totals.Lines,
			totals.Markup.DesignSystem,
			totals.Markup.Catalog,
			totals.Markup.Raw,
			totals.Markup.Local,
			totals.Markup.Lib,
			totals.Markup.SVG,
			formatInspectUIShare(totals.Markup.DSShare),
			totals.Style.TokenRefs,
			totals.Style.RawColors,
			totals.Style.RawSizes,
			formatInspectUIShare(totals.Style.TokenShare),
			totals.Style.InlineStyleProps,
			totals.Files,
		)
	}
	if len(response.Frontends) == 0 {
		_, _ = fmt.Fprintln(writer, "No frontends configured.")
	}
	return writer.Flush()
}

func formatInspectUIShare(value *float64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", *value*100)
}
