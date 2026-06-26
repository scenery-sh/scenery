# ZeroFS Legal Posture

ZeroFS is a Scenery-managed toolchain artifact, not an app-facing API. Apps use `scenery.sh/storage`; the pinned ZeroFS binary is a backing substrate for the current beta managed-storage path.

The pinned ZeroFS entry in `scenery.toolchain.json` is marked `AGPL-3.0-only`. Scenery storage must stay beta and must not be documented or released as production-ready until the project owner records an explicit legal/compliance decision for distributing and recommending that artifact in production.

Current decision:

- Managed ZeroFS is allowed for local development and production-readiness proof work.
- Production recommendation is blocked.
- The unblock must either approve the pinned AGPL artifact for the intended distribution model, replace it with a suitably licensed/commercial artifact, or move production storage to an operator-provided proxy whose license/compliance obligations are outside the Scenery binary release.

Release rule:

- If docs, plans, or release notes claim ZeroFS-backed Scenery storage is production-ready, this document must be updated in the same change with the owner-approved legal decision.
