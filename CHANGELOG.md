# Changelog

All notable changes in the Aspeternity-maintained fork are documented here.

## 1.5.1-asp.3 - 2026-09-17

### Added

- Added administrator-controlled historical anomaly scanning for persisted `traffic.up` and `traffic.down` rollups.
- Added a read-only preview that reports affected servers, exact rollup bucket count, fully sealed cleanup boundary, and bounded anomaly samples before any data can be removed.
- Added a guarded cleanup operation that deletes only contaminated historical rollup buckets while leaving cumulative `net.total.up` / `net.total.down` counters untouched.

### Safety

- Detects anomalies using each rollup bucket's `max_val` rather than aggregate `sum`, so legitimately large long-window traffic totals are not mistaken for one impossible report.
- Defaults to a conservative 1 TiB per-report threshold and enforces a 64 GiB minimum threshold.
- Derives the cleanup boundary from the largest configured rollup tier plus its sealing grace period, preventing an anomaly still present in an in-memory 5-minute/hour/day parent from being written back after cleanup.
- Requires explicit confirmation plus the preview's exact boundary, expected match count, and SHA-256 candidate-set fingerprint; any stale or changed preview fails closed and must be scanned again.
- Refuses a single cleanup operation above 10,000 persisted buckets.
- Records both preview and cleanup activity in the audit log.

### Maintenance

- Added storage-engine regression tests covering anomaly detection, targeted deletion, unrelated-metric preservation, cleanup blast-radius refusal, fully sealed rollup boundaries, and same-cardinality stale-preview rejection.
- Added `go test ./pkg/metric` to pull-request CI so persisted-rollup maintenance code is tested directly.

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
