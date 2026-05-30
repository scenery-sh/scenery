# Tech Debt

This file tracks known project debt that should be visible to agents before they start large edits.

## Open

### Full Dashboard Parity

- Area: dashboard
- Severity: medium
- Owner: onlava dashboard
- Created: 2026-04-27
- Review after: 2026-05-27

The editable dashboard source exists, but parity should continue to be verified visually for complex pages such as traces, API Explorer, Cron, and DB Explorer.

### Browser Harness

- Area: harness
- Severity: medium
- Owner: onlava runtime
- Created: 2026-04-27
- Review after: 2026-05-27

The self harness validates CLI, schemas, docs, and builds, but it does not yet run browser journeys or screenshot assertions against the dashboard.

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
