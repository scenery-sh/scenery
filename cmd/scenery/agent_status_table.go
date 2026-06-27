package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	localagent "scenery.sh/internal/agent"
)

func writeStatusTable(w io.Writer, sessions []localagent.Session, substrates []localagent.Substrate) {
	if len(sessions) == 0 {
		fmt.Fprintln(w, "No Scenery dev app roots found.")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "APP\tWORKTREE\tSTATUS\tURL\tSERVICES\tUPDATED")
		for _, session := range sessions {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				statusTableValue(firstNonEmpty(session.BaseAppID, session.RuntimeAppID, session.AppRoot)),
				statusTableValue(statusSessionWorktree(session)),
				statusTableValue(session.Status),
				statusTableValue(statusSessionURL(session)),
				statusTableValue(statusSessionServices(session)),
				statusTableUpdated(session.UpdatedAt),
			)
		}
		_ = tw.Flush()
	}
	if len(substrates) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Shared substrates:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tSTATUS\tOWNER PID\tCOMPONENT PIDS\tURLS")
	for _, substrate := range substrates {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			statusTableValue(substrate.Kind),
			statusTableValue(substrate.Status),
			statusTablePID(substrate.OwnerPID),
			statusTablePIDs(substrate.PIDs),
			statusTableURLs(substrate.URLs),
		)
	}
	_ = tw.Flush()
}

func statusSessionWorktree(session localagent.Session) string {
	if value := strings.TrimSpace(session.RouteManifest.Worktree); value != "" {
		return value
	}
	if value := strings.TrimSpace(session.Branch); value != "" {
		return value
	}
	return filepathBase(session.AppRoot)
}

func statusSessionURL(session localagent.Session) string {
	if value := strings.TrimSpace(session.RouteManifest.BaseURL); value != "" {
		return value
	}
	return statusSessionConsoleURL(session)
}

func statusSessionServices(session localagent.Session) string {
	routes := session.RouteManifest.Routes
	if len(routes) == 0 {
		return statusTableURLs(session.Routes)
	}
	names := make([]string, 0, len(routes))
	for name, record := range routes {
		if name == "root" || strings.TrimSpace(record.URL) == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+strings.TrimSpace(routes[name].URL))
	}
	return strings.Join(parts, ",")
}

func statusSessionConsoleURL(session localagent.Session) string {
	if session.Routes != nil {
		if value := strings.TrimSpace(session.Routes[localagent.RouteDashboard]); value != "" {
			return value
		}
	}
	if session.Aliases != nil {
		if value := strings.TrimSpace(session.Aliases[localagent.RouteDashboard]); value != "" {
			return value
		}
	}
	if session.RouteNamespace.Hosts != nil {
		if host := strings.TrimSpace(session.RouteNamespace.Hosts["console"]); host != "" {
			return host
		}
		if host := strings.TrimSpace(session.RouteNamespace.Hosts[localagent.RouteDashboard]); host != "" {
			return host
		}
	}
	return ""
}

func filepathBase(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path == "" {
		return ""
	}
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func statusTableValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func statusTablePID(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return fmt.Sprint(pid)
}

func statusTablePIDs(pids map[string]int) string {
	if len(pids) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(pids))
	for name, pid := range pids {
		if pid > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", name, pid))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func statusTableURLs(urls map[string]string) string {
	if len(urls) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(urls))
	for name, url := range urls {
		if url = strings.TrimSpace(url); url != "" {
			parts = append(parts, name+"="+url)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func statusTableUpdated(updated time.Time) string {
	if updated.IsZero() {
		return "-"
	}
	d := time.Since(updated)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return updated.Local().Format("2006-01-02")
	}
}
