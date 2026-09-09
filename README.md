# scenery

A runtime and development toolkit for Go applications.

Scenery helps you build an app without assembling all of its infrastructure
yourself. You write the business logic in Go. Scenery connects it to HTTP APIs,
databases, background jobs, and your frontend, then gives you one place to run
and debug it.

It is open source and runs on your own machine. It is not a hosted service.

## Why it exists

Building a product involves more than writing its features. You also have to
wire services together, keep frontend clients in sync with backend APIs, run
dependencies, and work out what happened when something fails.

Scenery brings that work into one tool. You describe what your app contains,
and Scenery uses that description to connect the pieces, generate matching
types and clients, and make the running app inspectable.

The goal is to spend more time on your product and less time maintaining the
plumbing around it.

## What you get

- **One local development command.** `scenery up` runs your backend, configured
  frontend dev servers, and managed dependencies, with file watching and a
  development dashboard.
- **Common backend capabilities.** Typed HTTP APIs, authentication, PostgreSQL,
  object storage, background jobs, and schedules.
- **A connected frontend.** Generated TypeScript clients match your declared
  APIs. Your product UI stays in your frontend.
- **Tools to understand the app.** Browse APIs, logs, traces, and metrics
  through the dashboard or CLI.

Scenery is aimed at developers building Go-backed applications who want these
pieces to work together. It is a runtime and toolchain for your code, not an
AI app generator.

## How it works

A Scenery app has three main ingredients:

1. **App declarations** in `app.scn` and `package.scn`: what services and
   operations exist, how they connect, and which capabilities they need.
2. **Go packages** implementing what those operations actually do.
3. **Runtime configuration** in `.scenery.json`: how to run the app and its
   frontends in each environment.

Scenery reads the declarations, generates Go types and TypeScript clients,
and runs the app. For an existing Scenery app, the everyday loop starts in
its directory:

```sh
scenery up
```

Keep it running while you edit. Use `scenery ps` to find the app's URLs,
`scenery logs --follow` to follow its logs, and `scenery down` to stop it.

## Working with AI agents

Agents can inspect the same app structure and runtime information that you
can. Machine-readable commands let them discover APIs, check changes, and
investigate failures without guessing how the project is wired.

An [installable agent skill](SKILL.md) explains the workflow:

```sh
npx skills add https://github.com/scenery-sh/scenery
```

You do not need an AI agent to use Scenery.

## Try it

Scenery is under active development. Its app format and CLI evolve together,
so upgrades can require application changes. Public deployment tooling is
currently beta.

### Install from source

You need Go 1.27+ and Bun. Apps using managed PostgreSQL also need Docker.

```sh
git clone https://github.com/scenery-sh/scenery.git
cd scenery
./scripts/build-dashboard-ui-embed.sh
go install ./cmd/scenery
scenery doctor
```

Make sure your Go bin directory is on `PATH`. Source builds are the supported
installation path; there are no prebuilt CLI releases.

### Explore an example

Start with the [webhook inbox example](examples/webhook-inbox/README.md).
It shows a small app that accepts an event, processes it in a background job,
stores the result, and exposes it through an authenticated API.

For your own app, see the [app development cookbook](docs/app-development-cookbook.md).

## Learn more

- [Documentation](docs/index.md) — guides and reference material.
- [CLI and runtime reference](docs/local-contract.md) — exact commands and behavior.
- [Architecture](ARCHITECTURE.md) — how Scenery itself is organized.
- [Contributing](CONTRIBUTING.md) — working on the project.

Report vulnerabilities privately using the [security policy](SECURITY.md).

Licensed under [Apache 2.0](LICENSE).
