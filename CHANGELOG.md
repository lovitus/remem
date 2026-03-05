# Changelog

## v0.3.1 - 2026-03-05

- Log system redesigned with categories:
  - Routine scan logs are rolling (default 100 lines)
  - Important logs are separate and retained in memory, including all kill events
- Log page redesigned to show categorized logs clearly.
- Rules editor redesigned for usability:
  - shows default rules and current effective rules
  - clearer guidance and examples
  - supports one-click clear of custom patch
- Rules API now returns default/effective/env/custom context for better UI clarity.

## v0.3.0 - 2026-03-05

- Added tray `Edit Rules` entry with web editor.
- Rules editor now saves and applies immediately (hot reload).
- Added persistent custom rules file support with default path:
  - macOS: `~/Library/Application Support/remem/rules.json`
  - Windows: `%AppData%\\remem\\rules.json`
- Added Windows-focused CPU optimizations:
  - group round-robin scanning (`REMEM_GROUPS_PER_SCAN`)
  - hot-group fast path (`REMEM_GROUP_HOT_RATIO`, `REMEM_GROUP_HOT_TTL_SEC`)
  - RSS collection only on relevant processes
- Replaced tray icon with a custom remem icon (not reused from systray sample).

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
