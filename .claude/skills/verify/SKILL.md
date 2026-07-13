---
name: verify
description: Drive scenery runtime changes through a real CLI or HTTP surface and capture observable evidence.
---

# Project Verification

Use the narrowest real surface touched by the diff. Do not substitute unit tests or typecheck for runtime observation.

## Standard-auth HTTP changes

There is no tracked standard-auth fixture. For cookie/session behavior, use a disposable live app:

1. Confirm the managed `scenery-postgres` container is running and read its user/password/host port from `docker inspect` without printing secrets.
2. Create a disposable database with `docker exec scenery-postgres psql`.
3. Create a temporary Go module outside the repo with:
   - `require scenery.sh v0.0.0`
   - `replace scenery.sh => <repo-root>`
   - a `main` that calls `auth.RegisterStandard(auth.StandardConfig{Enabled: true, AutoBootstrapDatabase: true})` and `runtime.Main(runtime.AppConfig{Name: "auth-verify", ListenAddr: <free-loopback-address>})`.
4. Run `go mod tidy` in the temporary module, then launch it with disposable `DATABASE_URL`, `JWT_SECRET`, and an empty `AUTH_COOKIE_DOMAIN`.
5. Drive the public HTTP surface:
   - `POST /auth/signup/email`, then `POST /auth/email-verification/confirm` using the local `dev_verification_token`.
   - Capture the issued `scenery_refresh` cookie.
   - Exercise `/auth/refresh` with legacy-only, both-present, empty-current, malformed-current, and invalid-current cookie headers.
   - Exercise `/auth/logout` with the valid legacy token and capture every `Set-Cookie` field value separately.
6. Stop the app, terminate remaining database connections, drop the disposable database, and remove the temporary module.

For bounded refresh-cookie compatibility, expected observations are:

- confirmation and every successful refresh issue only `scenery_refresh`;
- legacy-only refresh succeeds;
- both-present refresh uses the valid current cookie;
- empty or malformed current cookies return `refresh session is missing` without consuming the valid legacy session;
- an invalid current token returns `refresh session is invalid` without legacy fallback;
- logout returns `{"ok":true}` with separate clearing headers ordered `scenery_refresh`, then `onlv_refresh`.

If the managed Postgres container is unavailable, report verification as blocked rather than replacing this flow with tests.
