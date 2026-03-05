# Deploy Guide

## macOS

### Build

```bash
go build -o remem ./cmd/remem
```

### Run

```bash
./remem
```

### Auto-start (LaunchAgent)

Create `~/Library/LaunchAgents/com.lovitus.remem.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>com.lovitus.remem</string>
    <key>ProgramArguments</key>
    <array>
      <string>/ABS/PATH/remem</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
  </dict>
</plist>
```

Load:

```bash
launchctl load ~/Library/LaunchAgents/com.lovitus.remem.plist
```

## Windows

### Build

```powershell
go build -o remem.exe ./cmd/remem
```

### Run

```powershell
.\remem.exe
```

### Auto-start

Place shortcut in:

`%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup`

## Recommended defaults

```bash
REMEM_SCAN_INTERVAL_MS=3000
REMEM_COMMAND_LIMIT_GB=2
REMEM_GROUP_LIMIT_GB=6
```
