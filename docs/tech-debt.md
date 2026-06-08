# Tech Debt

This file tracks known project debt that should be visible to agents before they start large edits.

## Open

### onlava console TUI Broken Formatting

- Area: console
- Severity: medium
- Owner: onlava runtime
- Created: 2026-06-09
- Review after: 2026-07-09

`onlava console` TUI behavior is not working as expected. It causes weird terminal issues and the formatting is completely broken. Treat console/TUI changes as suspect until this is diagnosed and fixed with terminal-rendering coverage.

### Full Dashboard Parity

- Area: dashboard
- Severity: medium
- Owner: onlava dashboard
- Created: 2026-04-27
- Review after: 2026-05-27

The editable dashboard source exists, but parity should continue to be verified visually for complex pages such as traces, API Explorer, Cron, and DB Explorer.

### Browser Harness Fixture-Backed Mutation Depth

- Area: harness
- Severity: medium
- Owner: onlava runtime
- Created: 2026-06-07
- Review after: 2026-07-07

The browser UI harness now captures route-specific semantic journeys, screenshots, console events, network requests, and DOM snapshots for the core dashboard routes. Remaining debt is deeper fixture-backed mutation coverage for flows such as actually sending API Explorer requests, running DB queries against managed fixtures, clearing traces, and validating docs/help routes when those pages exist.

### Deeper Architecture Checks

- Area: harness
- Severity: low
- Owner: onlava runtime
- Created: 2026-04-27
- Review after: 2026-05-27

The self harness now enforces the first architecture checks: dependency allowlist, forbidden imports, CLI package boundaries, generated-file hygiene, and file-size thresholds. Future work can add deeper package dependency direction rules once the repo structure stabilizes.

### Long Build Tests

- Area: tests
- Severity: low
- Owner: onlava runtime
- Created: 2026-04-27
- Review after: 2026-05-27

Some full `go test ./...` runs still spend most time in build/package tests. Keep these real tests, but continue optimizing the build path rather than gating them away.
