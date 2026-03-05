# Changelog

## v0.2.0 - 2026-03-05

- Renamed project identity to `remem` (binary, module, docs, tray title, release assets).
- Improved scan scheduling to serial queue mode (non-overlapping, no ticker pile-up).
- Reduced scan overhead by removing per-process executable path probe from hot path.
- Added colorful tray icon support on Windows/macOS.
- Added configurable customizations:
  - `REMEM_EXTRA_COMMANDS`, `REMEM_REMOVE_COMMANDS`
  - `REMEM_EXTRA_GROUPS`, `REMEM_REMOVE_GROUPS`
  - `REMEM_CONFIG_PATH` JSON file patch support
- Improved Windows UX:
  - release binary built as `windowsgui` to avoid black console window
  - optional console keep via `REMEM_SHOW_CONSOLE=1`

## v0.1.1 - 2026-03-05

- Included main entrypoint source in repository.

## v0.1.0 - 2026-03-05

- Initial release.
