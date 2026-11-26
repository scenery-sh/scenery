---
name: verify
description: Drive scenery runtime changes through a real CLI or HTTP surface and capture observable evidence.
---

# Project Verification

Use the narrowest real surface touched by the diff. Do not substitute unit tests or typecheck for runtime observation.

## Standard-auth HTTP changes

Standard authentication is owned by the Rust application host (`sdk/rust/scenery-kernel`).
Run the automated host proof against a disposable managed PostgreSQL database:

```sh
go run ./scripts/verify --probe auth --summary --write
```

For manual live HTTP observation of cookie/session behavior, start a disposable Scenery app outside the repo with standard auth enabled (`authentication "standard" { provider = std.provider.standard_auth; scheme = "session" }` in `app.scn`, `"auth": {"enabled": true, "auto_bootstrap_database": true}` in `.scenery.json`) using `.scenery/harness/bin/scenery up`:

1. Confirm Docker is available so managed PostgreSQL starts cleanly, or pass a disposable `DATABASE_URL` with an empty schema.
2. Start the disposable app with `.scenery/harness/bin/scenery up --detach --wait ready -o json` (or build a bundle with `.scenery/harness/bin/scenery build --development` and run `<bundle>/scenery-kernel`).
3. Drive the public HTTP surface:
   - `POST /auth/signup/email`, then `POST /auth/email-verification/confirm` using the local `dev_verification_token`.
   - Capture the issued refresh cookie. A local runtime names it `app_refresh_<authority>` (normalized host/port) from its listen authority; deployed runtimes use `app_refresh`.
   - Exercise `POST /auth/refresh` with valid, missing, empty, malformed, and invalid refresh cookie headers under that name, and confirm a canonical `app_refresh` cookie is ignored locally.
   - Exercise `POST /auth/logout` with the valid token and capture the `Set-Cookie` field value.
4. Stop the app (`scenery down --all`) and remove the disposable directory.

Expected observations are:

- confirmation and every successful refresh issue only the runtime's refresh cookie name;
- missing or empty cookies return `refresh session is missing`;
- malformed or invalid tokens return `refresh session is invalid`;
- logout returns `{"ok":true}` with one clearing header under the same name.

If Docker / managed PostgreSQL is unavailable, report verification as blocked rather than replacing this flow with unit tests.
