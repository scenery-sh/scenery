# Issue tracker: Local Markdown

Issues and product specs for this repo live as Markdown files in `.scratch/`.

## Conventions

- One feature per directory: `.scratch/<feature-slug>/`
- The product spec is `.scratch/<feature-slug>/PRD.md`.
- Implementation issues are `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, numbered from `01`.
- Record `Status:` near the top of each issue using the [triage vocabulary](triage-labels.md). Wayfinding tickets use the execution states below instead.
- Comments and conversation history append under a `## Comments` heading.
- External pull requests are not a triage surface.

## When a skill says "publish to the issue tracker"

Create the appropriate Markdown file under `.scratch/<feature-slug>/`, creating the directory when needed.

## When a skill says "fetch the relevant ticket"

Read the referenced Markdown file. The user will normally provide its path or issue number.

## Wayfinding operations

The `/wayfinder` skill uses one map file with one child file per ticket.
These conventions apply when that workflow is requested; they do not authorize
subagents or replace the repository's ExecPlan requirements.

- **Map**: `.scratch/<effort>/map.md`, containing Notes, Decisions-so-far, and Fog.
- **Child ticket**: `.scratch/<effort>/issues/<NN>-<slug>.md`, numbered from `01`, with `Type:` (`research`, `prototype`, `grilling`, or `task`) and `Status:` fields.
- **Blocking**: a `Blocked by: <NN>, <NN>` line near the top. A ticket is unblocked when every listed issue has `Status: resolved`.
- **Status**: `open`, `claimed`, or `resolved`; these track execution, separately from the five triage roles.
- **Frontier**: scan the effort's `issues/` directory for `Status: open` tickets whose blockers are resolved; the first by number wins.
- **Claim**: set `Status: claimed` and save before beginning work.
- **Resolve**: append the answer under `## Answer`, set `Status: resolved`, then add a context pointer to the map's Decisions-so-far.
