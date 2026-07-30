# Phoenix Admin Alerts

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Go Reference](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![Discord](https://img.shields.io/badge/Discord-Join-5865F2?logo=discord&logoColor=white)](https://discord.gg/2BS7Z4FhJ)

A concurrent Go service that monitors email mailbox/organization quotas and password expiry states, and issues email alert notifications via RabbitMQ.

The service runs on a cron schedule, querying user/organizational state from a primary mail database, cross-referencing thresholds against an alert-state PostgreSQL database (to avoid redundant or duplicate notifications), and publishing alert payloads to a message broker for delivery.

This project is free software, licensed under the [GNU General Public License v3.0](LICENSE).

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Features](#features)
- [Project Structure](#project-structure)
- [Configuration](#configuration)
- [Database Schemas](#database-schemas)
- [Run Locally](#run-locally)
- [Docker Deployment](#docker-deployment)
- [RabbitMQ Message Payloads](#rabbitmq-message-payloads)
- [Documentation](#documentation)
- [Community](#community)
- [Contributing](#contributing)
- [Security](#security)
- [License](#license)

---

## Architecture Overview

1. **Cron Scheduler**: Runs periodically (schedule defined by `CRON_SCHEDULE`) and kicks off a processing pass. Pass `--once` (or set `RUN_ONCE=true`) to run a single pass and exit instead.
2. **Concurrency & Worker Pool**: Organizations are processed sequentially, fetching their domains; mailboxes are then streamed into a worker pool of **50 concurrent workers** for fast, parallel evaluation.
3. **Database Dual-Usage**:
   - **Primary Mail Database**: Source of truth for domains, organizations, and mailboxes. Also bulk-updated to flag expired passwords.
   - **Quota State Database**: Tracks whether an alert has already been sent for a specific threshold/entity combination, ensuring alert de-duplication.
4. **RabbitMQ Publisher**: Connects to the message broker with connection/publisher logging and automatic reconnection. Messages are published with persistent delivery and a `type: email` header to a dedicated queue.

---

## Features

- **Organization Quota Alerts**: Monitors organization storage/email-identity/chat quota utilization and alerts when consumption breaches configured percentage thresholds (e.g., `80%`, `85%`, `95%`).
- **Mailbox Password Expiry Alerts**: Evaluates days remaining before passwords expire (based on domain-level `max_password_age` properties) and sends warning alerts at specific day thresholds (e.g., `15`, `7`, `3` days remaining).
- **Alert State De-duplication**: Tracks alert state in a PostgreSQL database so alerts are sent exactly once per threshold breach, and resets automatically once usage drops or the password is renewed.
- **Bulk Status Updates**: Batches password expiry status changes into bulk writes against the primary database to minimize write-lock contention.

---

## Project Structure

The project follows the conventional Go [`cmd/` + `internal/`](https://go.dev/doc/modules/layout) layout. Every package carries a `godoc`-style package comment; run `go doc ./...` (or `go doc <package>`) to browse the API from the command line.

```
.
├── cmd/
│   └── app/                # main package: wiring, cron scheduling, CLI flags
├── internal/
│   ├── database/            # primary mail DB + quota/alert-state DB access
│   ├── logger/               # process-wide structured (slog) logger setup
│   ├── models/                # shared data types (Organization, Domain, Mailbox, ...)
│   ├── rabbitmq/               # RabbitMQ publisher used to deliver alerts
│   └── service/                  # alert-processing pipeline (worker pool, thresholds)
├── Dockerfile                # multi-stage build producing a static scratch-based image
├── docker-compose.yml         # app + quota Postgres companion for local/prod deployment
├── DOCKERHUB.md                # Docker Hub repository overview
└── .github/workflows/          # CI: build & publish the Docker image
```

---

## Configuration

Configuration is managed entirely through environment variables. Copy the template `.env-example` to `.env` to configure your local setup:

```bash
cp .env-example .env
```

### Environment Variables

| Variable | Description | Example |
| :--- | :--- | :--- |
| **Primary Database (Mail Service)** | | |
| `DB_HOST` | Hostname of the mail service database | `localhost` |
| `DB_PORT` | Port number of the mail service database | `5432` |
| `DB_DATABASE` | Database name for mail service | `mail_database` |
| `DB_USERNAME` | Username for mail service | `admin_user` |
| `DB_PASSWORD` | Password for mail service | `******` |
| **Quota DB (Alert Tracking)** | | |
| `QUOTA_DB_HOST` | Hostname of the quota tracking database | `localhost` / `phoenix-admin-alerts-db` |
| `QUOTA_DB_PORT` | Port number of the quota tracking database | `5439` / `5432` |
| `QUOTA_DB_NAME` | Database name for quota tracking | `quota_db` |
| `QUOTA_DB_USER` | Username for quota tracking | `quota_user` |
| `QUOTA_DB_PASSWORD` | Password for quota tracking | `******` |
| **Thresholds & Limits** | | |
| `QUOTA_THRESHOLDS` | Comma-separated list of organization quota percentage alerts | `80,85,95` |
| **RabbitMQ Configuration** | | |
| `RABBITMQ_URL` | AMQP connection string for RabbitMQ | `amqp://user:pass@host:5672/` |
| `RABBITMQ_QUEUE` | Queue name to publish notification payloads | `v3_notifications` |
| **Execution Schedule** | | |
| `CRON_SCHEDULE` | Standard cron expression controlling how often a processing pass runs | `0 3 * * *` (daily at 3 AM) |
| `RUN_ONCE` | If `true`, perform a single processing pass and exit instead of scheduling | `true` |

> The `POSTGRES_*` variables in `.env-example` configure the bundled `phoenix-admin-alerts-db` container in `docker-compose.yml` and should match `QUOTA_DB_*` above.

---

## Database Schemas

### Quota State Database Schema
The service automatically initializes/verifies the schema in the quota database on every startup:

```sql
CREATE TABLE IF NOT EXISTS admin_alerts (
    id SERIAL PRIMARY KEY,
    entity_type TEXT NOT NULL,         -- 'organization' or 'password_expiry'
    entity_id TEXT NOT NULL,           -- UUID string or email address
    threshold INTEGER NOT NULL,        -- Breached percentage or days remaining
    is_active BOOLEAN DEFAULT TRUE,    -- Whether the alert is currently active
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(entity_type, entity_id, threshold)
);
```

---

## Run Locally

### Prerequisites
- **Go**: Version `1.26` or higher
- **PostgreSQL**: Access to a primary mail DB & a quota DB instance
- **RabbitMQ Broker**

### Execution Steps
1. Clone the repository and navigate to its root directory.
2. Create and fill in your `.env` configuration file:
   ```bash
   cp .env-example .env
   ```
3. Run the service:
   ```bash
   go run ./cmd/app
   ```

---

## Docker Deployment

The application includes a production-ready multi-stage `Dockerfile` and a `docker-compose.yml` defining the Go application and a PostgreSQL companion image for alert state tracking. Pre-built images are published to [Docker Hub](https://hub.docker.com/r/rjyspl/phoenix-admin-alerts) — see [DOCKERHUB.md](DOCKERHUB.md) for available tags and the pull command.

### Build and Run with Docker Compose

1. **Start Services**:
   ```bash
   docker compose up -d --build
   ```
   This will:
   - Create a PostgreSQL container (`phoenix-admin-alerts-db`) running on internal port `5432` (mapped to `127.0.0.1:5439` on the host).
   - Build a lightweight `scratch`-based image containing the compiled, statically linked Go binary.
   - Run the service container, linking it to the database companion.

2. **Check Logs**:
   ```bash
   docker compose logs -f phoenix-admin-alerts
   ```

3. **Shutdown Services**:
   ```bash
   docker compose down
   ```

---

## RabbitMQ Message Payloads

Notifications are sent as JSON payloads to the configured RabbitMQ queue.

### Payload Schema

```json
{
  "to": "recipient@domain.com",
  "template": "template_name",
  "variables": { ... }
}
```

- **Headers**: Published with the AMQP header `type` set to `email` and the Content-Type set to `application/json`.
- **Organization Quota Warning**:
  - `template`: `org_quota_warning`
  - `variables`:
    ```json
    {
      "name": "Organization Name",
      "threshold": "85",
      "storage_usage_percent": "86.42",
      "allocated_quota": "100.00",
      "utilized_quota": "86.42"
    }
    ```
- **Password Expiry Warning**:
  - `template`: `password_expiry_warning`
  - `variables`:
    ```json
    {
      "name": "User Name",
      "email": "user@domain.com",
      "days_remaining": 7,
      "expiry_date": "2026-07-20",
      "last_updated": "2026-04-20"
    }
    ```

---

## Documentation

All packages carry `godoc`-style package and exported-symbol comments, in keeping with the [Go standard library](https://go.dev/doc/comment) conventions. Browse them locally with:

```bash
go doc ./...
go doc github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/service
```

---

## Community

Join the [Yukthi Systems Discord](https://discord.gg/2BS7Z4FhJ) to ask questions, discuss the project, or get help running it.

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow, coding conventions, and how to submit changes. This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md).

## Security

Because this service handles database credentials and RabbitMQ connection strings, never commit a populated `.env` file. If you discover a security vulnerability, please report it privately to the maintainers rather than opening a public issue.

## License

Phoenix Admin Alerts is licensed under the [GNU General Public License v3.0](LICENSE) or (at your option) any later version.

```
Copyright (C) 2026 Yukthi Systems Private Limited

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU General Public License for more details.
```
