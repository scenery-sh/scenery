# Shared assistant archives emitted duplicate Go declarations

Status: resolved

ONLV's three-assistant artifact build failed with duplicate `nodeArchive_*` and
`nodeDescriptorJSON_*` declarations. Archive paths were deduplicated but their
embed declarations were emitted once per assistant. Descriptors also incorrectly
shared capsule-content identity when separate assistants used identical capsules.

## Resolution

`RenderAssistantAssetRegistry` emits each embed variable once and keys assistant
descriptors by their complete JSON digest. The existing in-process Go typecheck
now renders three assistants sharing Node and capsule assets. ONLV's real artifact
build with the development target and all nine aggregate harness checks pass.

## Validation

`go test ./...`, generator fixtures, TypeScript conformance/checks,
`golangci-lint run ./...`, and the quick self-harness passed on 2026-09-06.
Evidence: `/tmp/designer-runtime-scenery-*` and ONLV `/tmp/designer-runtime-harness.json`.
