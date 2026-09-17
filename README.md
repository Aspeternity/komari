# Komari — Aspeternity Maintained Fork

[English](./README.md) | [简体中文](./README_zh-cn.md)

Komari is a lightweight, self-hosted server monitoring solution with a Web dashboard and lightweight Agents.

> [!IMPORTANT]
> This repository is an independent maintenance fork of the original `komari-monitor/komari` project. It is based on upstream `1.5.0-fix1` and is not an official continuation endorsed by the original authors.

## Maintained build

- **Current maintenance version:** `1.5.1-asp.1`
- **Backend:** `Aspeternity/komari`
- **Frontend:** `Aspeternity/komari-web`
- **Production frontend branch:** `stable`
- **Docker image:** `ghcr.io/aspeternity/komari`
- **Persistent data:** `/app/data`

The production Docker build embeds the maintained `komari-web:stable` frontend instead of following the upstream frontend automatically.

## Maintenance highlights

This fork currently adds maintenance-focused changes while keeping the existing Komari database, Agent protocol and deployment model compatible:

- protects `/admin` and `/terminal` from third-party theme routing overrides;
- prevents third-party root Service Workers from taking over protected routes;
- cleans up stale theme Service Workers after upgrades/theme changes;
- avoids stale dynamic HTML after switching themes;
- builds against the maintained frontend fork with reproducible `npm ci` installs;
- automatically rebuilds the Docker image when the production frontend changes.

See [CHANGELOG.md](./CHANGELOG.md) and [MAINTENANCE.md](./MAINTENANCE.md) for details.

## Features

- **Real-time monitoring** with second-level updates.
- **Lightweight Agents** suitable for VPS and home-lab servers.
- **Self-hosted** monitoring and data ownership.
- **Web dashboard** for status, history, terminal and administration.
- **Custom themes and plugins** with maintained theme/admin isolation.

## Docker deployment

```yaml
services:
  komari:
    image: ghcr.io/aspeternity/komari:latest
    container_name: Komari
    restart: unless-stopped
    ports:
      - "25774:25774"
    volumes:
      - ./data:/app/data
```

Start or update with:

```bash
docker compose pull
docker compose up -d
```

For reproducible deployments, pin a maintenance version instead of `latest`:

```yaml
image: ghcr.io/aspeternity/komari:1.5.1-asp.1
```

If migrating from the upstream Docker image, keep the existing `/app/data` mapping unchanged.

## Versioning

Maintained versions use:

```text
<compatible-version>-asp.<revision>
```

For example:

```text
1.5.1-asp.1
```

Normal `main` Docker builds read the version from the repository `VERSION` file. GitHub Release builds use the release tag, and snapshot builds keep a separate timestamped prerelease version.

## Development flow

Backend changes are developed on feature/fix branches and merged into `main` after validation. Frontend work is developed on `Aspeternity/komari-web:radix`; validated production frontend changes are promoted to `stable`, which is the only frontend branch consumed by production backend builds.

## Upstream and license

Original project: `komari-monitor/komari`

Original frontend: `komari-monitor/komari-web`

The original project is licensed under the MIT License. The original copyright and license notices are preserved in this fork. This maintained fork is provided independently and without warranty.

> [!WARNING]
> Komari includes monitoring and remote-control capabilities. Deploy it only on systems you own or are authorized to manage.
