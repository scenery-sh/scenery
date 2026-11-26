package main

import (
	"fmt"
	"io"
	"time"
)

// Render the same observations returned by JSON. Inspection does not probe
// application readiness or turn source freshness into a process-health claim.
func writeWorktreeStatusText(w io.Writer, entries []worktreeStatusEntry) {
	for _, entry := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s (root=%s source=%s owner=%s metadata=%s freshness=%s)\n", firstNonEmpty(entry.AppRoot, entry.Key), entry.Status, entry.RootStatus, entry.SourceStatus, entry.OwnerStatus, entry.MetadataStatus, entry.SourceFreshness)
		writeStatusTable(w, entry.Sessions, nil)
		for _, session := range entry.Sessions {
			if session.StatusReason != "" {
				_, _ = fmt.Fprintf(w, "  %s: %s\n", session.SessionID, session.StatusReason)
			}
		}
		if len(entry.Serving) == 0 {
			_, _ = fmt.Fprintln(w, "  serving identity: unverified")
		}
		for _, serving := range entry.Serving {
			writeServingIdentity(w, serving)
		}
		for _, block := range entry.BuildBlocks {
			_, _ = fmt.Fprintf(w, "  session %s:\n", block.SessionID)
			writeRuntimeBuildBlock(w, &runtimeBuildBlock{Reason: block.Reason, Cause: block.Cause, Since: block.Since.Format(time.RFC3339Nano), PreventedBuilds: block.PreventedBuilds})
		}
	}
}

func writeServingIdentity(w io.Writer, serving runtimeServingIdentity) {
	_, _ = fmt.Fprintf(w, "  serving generation=%d pid=%s published=%s\n    source=%s\n    contract=%s implementation=%s\n    build_input=%s framework_source=%s\n", serving.Generation, serving.PID, serving.PublishedAt, serving.SourceSnapshotDigest, serving.ContractRevision, serving.ImplementationRevision, serving.BuildInputDigest, serving.FrameworkSourceDigest)
}

func writeRuntimeBuildBlock(w io.Writer, block *runtimeBuildBlock) {
	if block == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "  builds BLOCKED (%s) since %s, %d build(s) prevented: %s\n", block.Reason, block.Since, block.PreventedBuilds, block.Cause)
}

func writeSourceWaitText(w io.Writer, result runtimeSourceWait) {
	generation := uint64(0)
	if result.Serving != nil {
		generation = result.Serving.Generation
	}
	_, _ = fmt.Fprintf(w, "%s after %dms (target %s, generation %d, freshness %s)\n", result.Outcome, result.WaitedMS, result.TargetSourceSnapshotDigest, generation, result.SourceFreshness)
	if result.CurrentSourceSnapshotDigest != "" {
		_, _ = fmt.Fprintf(w, "  current source=%s\n", result.CurrentSourceSnapshotDigest)
	}
	if result.Serving != nil {
		writeServingIdentity(w, *result.Serving)
	} else {
		_, _ = fmt.Fprintln(w, "  serving identity: unverified")
	}
	writeRuntimeBuildBlock(w, result.BuildBlock)
	if result.CompileError != "" {
		_, _ = fmt.Fprintln(w, result.CompileError)
	}
}
