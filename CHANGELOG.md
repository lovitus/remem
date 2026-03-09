# Changelog

## v0.3.6 - 2026-03-08

- Changed rules-page action bar from sticky to true floating fixed toolbar on desktop.
- Top-right action buttons now remain visible while scrolling long pages.
- Preserved responsive fallback on small screens (normal flow layout).

## v0.3.5 - 2026-03-08

- Moved the three key actions to the top-right of Rules Editor:
  - `保存并立即生效`
  - `重新加载当前生效内容`
  - `恢复默认并生效`
- Action bar is now sticky on desktop to reduce missed saves while editing long rule lists.
- On mobile/small widths, action bar gracefully falls back to normal flow.

## v0.3.4 - 2026-03-08

- Added rule-level memory limit configuration in `rules.json`:
  - global overrides: `limits.commandGiB`, `limits.groupGiB`
  - per-command overrides: `commands.limitsGiB.<name>`
  - per-group overrides: `groups.limitsGiB.<name>`
- Monitor now applies effective limits from rules at runtime and hot-reload.
- Rules editor redesigned to a big-box item UI:
  - command/group items shown in one container each
  - per-item remove button `x`
  - bottom add row with `+`
  - per-item optional limit input (GiB)
  - editable global limit inputs (GiB)
- Updated docs with new JSON schema examples for limit overrides.

## v0.3.3 - 2026-03-05

- Rules editor interaction redesigned around direct editing of final effective rules:
  - users edit only two lists (commands/groups), no Add/Remove mental model exposed
  - per-row `-` remove action retained
  - trailing empty row retained automatically
  - non-empty row shows green `✓`
  - save still applies immediately (hot reload)
- Rules page now clearly separates:
  - current effective commands/groups
  - editable final commands/groups
  - default commands/groups reference
- Added explicit `重新加载当前生效内容` action to refresh editor state from runtime.

## v0.3.2 - 2026-03-05

- Routine log retention set to 10 lines by default.
- Important log retention set to 100 lines by default, including kill events.
- Rules editor UI redesigned to be more usable:
  - row-based editable lists with `-` remove buttons
  - auto trailing empty row
  - green check marker for non-empty row values
  - explicit `恢复默认` action
  - clear visual status for default/effective command and group rules
- Save still applies immediately via hot reload.

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
