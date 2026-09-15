# Native Build Driver Decision

Status: NO-GO. Do not promote the equal-capture retained driver.

## Decision

The retained driver did not materially reduce the complete full-ONLV build or
accepted-edit transaction when both lanes performed the same complete package
discovery, input hashing, and immutable capture. The experiment compiled the
same generated 620-package ONLV application in both lanes; it was not the
earlier small AHJ island.

The current evidence is
`.scenery/harness/native-build-driver/20260914T213630Z-9d8b2dff278a8b9e/report.json`.
It contains 60 stock and 60 driver samples across two independently bootstrapped
cohorts. The watchdog interrupted the non-decision churn tail after 48 complete
authenticated edits: generation 49 still has a successful retained build
artifact, and edit 50 was explicitly waived rather than repeating the hour-long
experimental run. The report preserves this distinction and the original
watchdog report.

| Boundary | Stock p50 / p95 | Driver p50 / p95 |
|---|---:|---:|
| Complete input capture | 1,132.701 / 1,345.478 ms | 1,067.318 / 1,278.086 ms |
| Accountable build | 2,352.273 / 3,048.912 ms | 2,268.237 / 2,677.773 ms |
| First verified response | 5,565.044 / 6,273.784 ms | 5,441.225 / 6,141.234 ms |
| Accepted edit | 7,518.618 / 8,253.349 ms | 7,420.941 / 8,252.804 ms |

The candidate reduced accountable-build p50 by only 84.036 ms (3.6 percent),
short of both the frozen 100 ms and 25 percent gates. Accepted-edit p50 improved
by only 97.678 ms (1.3 percent). Stage II was therefore not run. All benchmark
source mutations occurred in pinned detached worktrees, the target digest and
runtime identity checks passed, and owned worktrees and processes were removed.

The later [retained compiler experiment](native-build-compiler-decision.md)
keeps the complete input domain but removes repeated Go discovery. Its evidence
is separate and must not be attributed to this rejected protocol. Neither
experiment automatically selects a production build backend.
