# Contributing to Phoenix Admin Alerts

Thanks for your interest in improving Phoenix Admin Alerts! This document covers how to get a development environment running, the conventions the codebase follows, and how to submit changes.

By contributing, you agree that your contributions will be licensed under the project's [GNU General Public License v3.0](LICENSE).

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold it.

## Getting Started

### Prerequisites

- Go 1.26 or later
- Access to a PostgreSQL instance (primary mail DB + quota DB)
- A RabbitMQ broker

### Local Setup

```bash
git clone https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts.git
cd Phoenix-Admin-Alerts
cp .env-example .env   # fill in your local database/broker credentials
go run ./cmd/app --once
```

`docker compose up -d --build` also works if you'd rather run the quota database in a container; see the [README](README.md#docker-deployment) for details.

## Making Changes

1. **Open an issue first** for anything beyond a trivial fix (typos, doc corrections), so the change can be discussed before you invest time in it.
2. **Create a branch** off `main` named after the change, e.g. `fix/cron-schedule-env-var`.
3. **Keep changes focused.** Prefer several small, reviewable PRs over one large one.
4. **Write Go doc comments** for new exported types, functions, and packages, following the conventions already used throughout `internal/` — see the [Go Doc Comments](https://go.dev/doc/comment) guide. Run `go doc ./...` to sanity-check how they render.
5. **Match existing style.** No unnecessary abstractions, no dead/commented-out code, no speculative configuration options.

### Before opening a PR

Run the standard Go toolchain checks locally:

```bash
gofmt -l .          # should print nothing
go vet ./...
go build ./...
```

If you're adding tests, `go test ./...` should also pass.

### Commit messages

Write commit messages that explain *why* a change was made, not just what changed. Reference the related issue where relevant (e.g. `Fixes #12`).

### Pull requests

- Describe the problem being solved and how you solved it.
- Note any manual testing you performed (this project currently has limited automated test coverage, so a clear description of how you verified the change matters).
- Update `README.md` / `DOCKERHUB.md` if your change affects configuration, environment variables, or deployment.

## Reporting Bugs

Open a [GitHub issue](https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts/issues) including:

- What you expected to happen vs. what actually happened
- Steps to reproduce (config, environment, relevant logs)
- Go version and how you're running the service (binary, Docker, etc.)

Please do not include real credentials, connection strings, or other secrets in issues or PRs — see [SECURITY](README.md#security).

## Questions

If something in this guide is unclear, open an issue — improving this document is itself a welcome contribution. For quicker back-and-forth, ask in the [Yukthi Systems Discord](https://discord.gg/2BS7Z4FhJ).
