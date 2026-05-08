#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

mkdir -p auth/db/gen
printf '%s\n\n' '-- GENERATED: do not edit. Run `scripts/gen-auth-sqlc.sh` to refresh.' > auth/db/gen/schema.sql
atlas schema inspect --url file://auth/db/schema.hcl --dev-url docker://postgres/18/dev --format '{{ sql . }}' >> auth/db/gen/schema.sql
sqlc generate
