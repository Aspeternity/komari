# Changelog

All notable changes in the Aspeternity-maintained fork are documented here.

## 1.5.1-asp.2 - 2026-09-17

### Fixed

- Added persisted `memory.total`, `swap.total`, and `disk.total` metrics so historical capacity queries no longer fail with unknown metric keys.
- Restored uptime-aware restart detection that was lost during the upstream v2-only protocol migration.
- Suppressed implausibly large traffic deltas when uptime regresses and the cumulative counter jumps to a new domain, preventing restart/counter-domain changes from becoming TB/PB-scale history spikes while preserving small legitimate restart-boundary deltas.

### Maintenance

- Added regression coverage for historical capacity metrics and reboot-boundary traffic handling.
- Included the new capacity metrics in system-record cleanup and retention handling while keeping the legacy `models.Record` reconstruction shape unchanged.
- Made pull-request CI execute `go test ./internal/metricstore` so data-correctness regressions are tested rather than only compiled.

## 1.5.1-asp.1 - 2026-09-17

Based on upstream Komari `1.5.0-fix1`.

### Fixed

- Isolated `/admin` and `/terminal` routes from third-party themes.
- Prevented third-party root-scoped Service Workers from taking over protected routes.
- Added cleanup for stale Service Workers left by previously installed themes.
- Prevented protected-page assets from being overridden by active themes.
- Disabled reusable dynamic HTML caching where it caused stale theme/admin pages after theme switches.

### Maintenance

- Forked and pinned the production frontend to `Aspeternity/komari-web:stable`.
- Added frontend build validation with `npm ci` for reproducible dependency installs.
- Added automatic frontend-to-backend synchronization for production Docker rebuilds.
- Removed obsolete upstream-following and legacy deployment workflows from the maintained frontend.
- Added explicit maintenance versioning and Docker version tags.
