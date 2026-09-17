# Changelog

All notable changes in the Aspeternity-maintained fork are documented here.

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
