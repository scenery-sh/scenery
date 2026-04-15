# Dashboard Rewrite Inventory

The old runtime dashboard bundle in `cmd/pulse/devdash_static` remains a reference corpus only.

The first source rewrite slice covers:
- app shell and navigation
- live app status
- process output history plus live output
- traces list and trace detail
- stored requests CRUD
- service metadata
- API explorer
- cron listing
- database empty state / basic shell

Current backend contracts used by the source app:
- WebSocket JSON-RPC: `version`, `list-apps`, `status`, `process/output/list`, `traces/list`, `traces/get`, `api-call`, `db/query`, `db/transaction`, `editors/list`, `editors/open`
- WebSocket notifications: `process/start`, `process/compile-start`, `process/reload`, `process/stop`, `process/compile-error`, `process/output`, `trace/new`
- GraphQL: `getStoredRequests`, `createStoredRequest`, `updateStoredRequest`, `deleteStoredRequest`

Routes intentionally not recreated from Encore:
- cloud dashboard / deploy entry points
- Clerk / remote auth UX
- marketing / onboarding promo flows
- unsupported cloud-only feature pages
