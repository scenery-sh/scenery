# Tech Debt

This file tracks known project debt that should be visible to agents before they start large edits.

## Open

### scenery console TUI Broken Formatting

- Area: console
- Severity: medium
- Owner: scenery runtime
- Created: 2026-06-09
- Review after: 2026-07-09

2026-06-12 update: the "completely frozen" symptom was the devdash store incident — a 422 MB `devdash.json` made every 500 ms TUI refresh re-parse the whole file for ~5 s (`scenery logs --limit 1` took 5.4 s; 0.21 s after compaction). That cause is fixed (process-event payload cap) and the structural follow-up is ExecPlan 0076. Remaining debt here is the terminal-rendering quality itself: raw escape-sequence redraw (`\x1b[2J` full clears each tick in `cmd/scenery/dev_console.go`) flickers and misformats on resize and narrow terminals. Fix needs terminal-rendering coverage, not store work.

### Full Dashboard Parity

- Area: dashboard
- Severity: medium
- Owner: scenery dashboard
- Created: 2026-04-27
- Review after: 2026-05-27

The editable dashboard source exists, but parity should continue to be verified visually for complex pages such as traces, API Explorer, Cron, and DB Explorer.

### Browser Harness Fixture-Backed Mutation Depth

- Area: harness
- Severity: medium
- Owner: scenery runtime
- Created: 2026-06-07
- Review after: 2026-07-07

The browser UI harness now captures route-specific semantic journeys, screenshots, console events, network requests, and DOM snapshots for the core dashboard routes. Remaining debt is deeper fixture-backed mutation coverage for flows such as actually sending API Explorer requests, running DB queries against managed fixtures, clearing traces, and validating docs/help routes when those pages exist.

### Deeper Architecture Checks

- Area: harness
- Severity: low
- Owner: scenery runtime
- Created: 2026-04-27
- Review after: 2026-05-27

The self harness now enforces the first architecture checks: dependency allowlist, forbidden imports, CLI package boundaries, generated-file hygiene, and file-size thresholds. Future work can add deeper package dependency direction rules once the repo structure stabilizes.

### Long Build Tests

- Area: tests
- Severity: low
- Owner: scenery runtime
- Created: 2026-04-27
- Review after: 2026-05-27

Some full `go test ./...` runs still spend most time in build/package tests. Keep these real tests, but continue optimizing the build path rather than gating them away.
