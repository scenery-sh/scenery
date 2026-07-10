# Issue tracker: Local Markdown

Issues and product specs for this repo live as Markdown files in `.scratch/`.

## Conventions

- One feature per directory: `.scratch/<feature-slug>/`
- The product spec is `.scratch/<feature-slug>/PRD.md`.
- Implementation issues are `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, numbered from `01`.
- Triage state is recorded as a `Status:` line near the top of each issue file; use the values in `docs/agents/triage-labels.md`.
- Comments and conversation history append under a `## Comments` heading.
- External pull requests are not a triage surface.

## When a skill says "publish to the issue tracker"

Create the appropriate Markdown file under `.scratch/<feature-slug>/`, creating the directory when needed.

## When a skill says "fetch the relevant ticket"

Read the referenced Markdown file. The user will normally provide its path or issue number.

## Wayfinding operations

The `/wayfinder` skill uses one map file with one child file per ticket.

- **Map**: `.scratch/<effort>/map.md`, containing Notes, Decisions-so-far, and Fog.
- **Child ticket**: `.scratch/<effort>/issues/<NN>-<slug>.md`, numbered from `01`, with `Type:` (`research`, `prototype`, `grilling`, or `task`) and `Status:` fields.
- **Blocking**: a `Blocked by: <NN>, <NN>` line near the top. A ticket is unblocked when every listed issue has `Status: resolved`.
- **Frontier**: scan the effort's `issues/` directory for open, unblocked, unclaimed tickets; the first by number wins.
- **Claim**: set `Status: claimed` and save before beginning work.
- **Resolve**: append the answer under `## Answer`, set `Status: resolved`, then add a context pointer to the map's Decisions-so-far.
