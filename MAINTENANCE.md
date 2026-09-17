# Maintenance policy

This repository is an independent maintenance fork of the archived upstream project `komari-monitor/komari`.

## Baseline

- Upstream baseline: `1.5.0-fix1`
- Upstream commit: `0ca87aafd184ed75f9030ede0902772142af5eec`
- Maintained backend: `Aspeternity/komari`
- Maintained frontend: `Aspeternity/komari-web`
- Production frontend branch: `stable`
- Container image: `ghcr.io/aspeternity/komari`

## Versioning

Maintained releases use the following scheme:

```text
<upstream-compatible-version>-asp.<revision>
```

Example:

```text
1.5.1-asp.1
```

`VERSION` is the source of truth for normal `main` Docker builds. Release builds embed the GitHub release tag. Snapshot builds keep their own timestamped prerelease version.

## Branch model

Backend:

```text
feature/* or fix/*
        -> pull request
main
        -> production Docker build
```

Frontend:

```text
radix
  -> development and CI validation
stable
  -> production frontend consumed by Aspeternity/komari
```

The backend build always consumes `Aspeternity/komari-web:stable` so upstream frontend changes cannot silently alter production images.

## Maintenance scope

The goal is to preserve compatibility with existing Komari installations while fixing bugs and adding practical improvements. Prefer changes that do not break:

- existing databases and migrations;
- Agent protocol compatibility;
- public RPC/API behavior unless a bug requires correction;
- existing themes and plugins where practical;
- Docker bind mounts under `/app/data`.

Potentially breaking changes should be documented in `CHANGELOG.md` before release.

## Security and attribution

The original Komari copyright and MIT license remain intact. This fork is independently maintained and is not an official continuation endorsed by the original authors.
