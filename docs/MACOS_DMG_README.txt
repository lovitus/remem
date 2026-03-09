remem macOS Install Guide

1) Install
- Open the DMG file.
- Drag "remem.app" into "Applications".

2) First launch
- Open "Applications/remem.app".
- If macOS blocks it, do either:
  - Right-click remem.app -> Open -> Open
  - Or use System Settings (below).

3) Remove quarantine flag (Terminal)
- Run:
  xattr -dr com.apple.quarantine "/Applications/remem.app"

4) Allow in System Settings
- Open System Settings -> Privacy & Security.
- Find the blocked item for remem near the bottom.
- Click "Open Anyway", then confirm.

Notes
- This app is currently not notarized.
- If you update the app, macOS may ask again for permission.
