# Phoenix Admin Alerts

A concurrent Go service that monitors email mailbox/organization quotas and password expiry states, and issues email alert notifications via RabbitMQ.

It runs on a cron schedule, cross-references thresholds against a PostgreSQL alert-state database to avoid duplicate notifications, and publishes alert payloads to RabbitMQ for delivery.

- **Source**: [github.com/Yukthi-Systems/Phoenix-Admin-Alerts](https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts)
- **License**: [GNU General Public License v3.0](https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts/blob/main/LICENSE)
- **Issues**: [github.com/Yukthi-Systems/Phoenix-Admin-Alerts/issues](https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts/issues)

## Supported Tags

Images are built and published automatically by the [`docker-image.yml`](https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts/blob/main/.github/workflows/docker-image.yml) GitHub Actions workflow on every push to `main` and every version tag, for `linux/amd64`.

| Tag | Description |
| :--- | :--- |
| `latest` | Most recent build of the `main` branch. |
| `<git-tag>` (e.g. `v1.2.0`) | Immutable build from the corresponding Git tag — recommended for production. |

## Quick Start

Pull the image:

```bash
docker pull rjyspl/phoenix-admin-alerts:latest
```

Run it with an `.env` file (see [Configuration](#configuration) below):

```bash
docker run -d \
  --name phoenix-admin-alerts \
  --env-file .env \
  --restart always \
  rjyspl/phoenix-admin-alerts:latest
```

### Docker Compose

A minimal `docker-compose.yml` that also runs the companion quota-tracking PostgreSQL database:

```yaml
services:
  phoenix-admin-alerts-db:
    image: postgres:16-alpine
    container_name: phoenix-admin-alerts-db
    restart: always
    env_file:
      - .env
    volumes:
      - pgdata:/var/lib/postgresql/data

  phoenix-admin-alerts:
    image: rjyspl/phoenix-admin-alerts:latest
    container_name: phoenix-admin-alerts
    restart: always
    env_file:
      - .env
    depends_on:
      - phoenix-admin-alerts-db

volumes:
  pgdata:
```

```bash
docker compose up -d
```

## Configuration

The image is configured entirely through environment variables — no config files or mounted volumes are required.

| Variable | Description | Example |
| :--- | :--- | :--- |
| `DB_HOST` / `DB_PORT` / `DB_DATABASE` / `DB_USERNAME` / `DB_PASSWORD` / `DB_SSLMODE` | Primary mail service database connection | `localhost`, `5432`, `mail_database`, ..., `disable` |
| `QUOTA_DB_HOST` / `QUOTA_DB_PORT` / `QUOTA_DB_NAME` / `QUOTA_DB_USER` / `QUOTA_DB_PASSWORD` | Alert-tracking (quota state) database connection | `phoenix-admin-alerts-db`, `5432`, `quota_db`, ... |
| `QUOTA_THRESHOLDS` | Comma-separated organization quota percentage alerts | `80,85,95` |
| `RABBITMQ_URL` | AMQP connection string used to publish alert emails | `amqp://user:pass@host:5672/` |
| `RABBITMQ_QUEUE` | Queue name for notification payloads | `v3_notifications` |
| `CRON_SCHEDULE` | Standard cron expression for how often a pass runs | `0 3 * * *` |
| `RUN_ONCE` | Set to `true` to run a single pass and exit (useful for one-off runs / Kubernetes CronJobs) | `true` |

Full details, including the database schema the image auto-creates, are documented in the project [README](https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts#readme).

## Image Details

- Built from a multi-stage `Dockerfile`: compiled with `CGO_ENABLED=0` on `golang:1.26-alpine`, then copied into a minimal `alpine:latest` runtime image alongside CA certificates and timezone data.
- Ships a `HEALTHCHECK` that verifies the process is running.
- Runs as a single static binary with no external runtime dependencies beyond the PostgreSQL databases and RabbitMQ broker it connects to.

## License

This image is built from free software licensed under the [GNU General Public License v3.0](https://github.com/Yukthi-Systems/Phoenix-Admin-Alerts/blob/main/LICENSE). You are free to use, inspect, modify, and redistribute it under the same terms.
