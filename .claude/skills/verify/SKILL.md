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
   - Capture the issued refresh cookie. A local runtime names it `scenery_refresh_<host>_<port>` after its listen address; deployed runtimes use `scenery_refresh`.
   - Exercise `/auth/refresh` with valid, missing, empty, malformed, and invalid refresh cookie headers under that name, and confirm a canonical `scenery_refresh` cookie is ignored locally.
   - Exercise `/auth/logout` with the valid token and capture the `Set-Cookie` field value.
6. Stop the app, terminate remaining database connections, drop the disposable database, and remove the temporary module.

Expected observations are:

- confirmation and every successful refresh issue only the runtime's refresh cookie name;
- missing, empty, or malformed cookies return `refresh session is missing`;
- an invalid current token returns `refresh session is invalid`;
- logout returns `{"ok":true}` with one clearing header under the same name.

If the managed Postgres container is unavailable, report verification as blocked rather than replacing this flow with tests.
