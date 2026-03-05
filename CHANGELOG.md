# Changelog

## v0.1.0 - 2026-03-05

- Initial release.
- Implemented cross-platform (macOS/Windows) guard daemon with tray UI.
- Added command-level memory fuse (default 2 GiB) for common shell/editor/text tools.
- Added app-group memory fuse (default 6 GiB) for Codex, Windsurf, VS Code, Antigravity, and major browsers.
- Added non-overlapping scan scheduler to avoid scan pile-ups.
- Added in-memory log ring buffer + live local log page.
- Added release packaging script and GitHub release workflow.
