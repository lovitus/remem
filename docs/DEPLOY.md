# Deploy Guide

## macOS

### 1) Build

```bash
go build -o remem-guard ./cmd/remem-guard
```

### 2) Run

```bash
./remem-guard
```

### 3) Auto-start (LaunchAgent)

Create `~/Library/LaunchAgents/com.lovitus.remem-guard.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>com.lovitus.remem-guard</string>
    <key>ProgramArguments</key>
    <array>
      <string>/ABS/PATH/remem-guard</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
  </dict>
</plist>
```

Load:

```bash
launchctl load ~/Library/LaunchAgents/com.lovitus.remem-guard.plist
```

## Windows

### 1) Build

```powershell
go build -o remem-guard.exe ./cmd/remem-guard
```

### 2) Run

```powershell
.\remem-guard.exe
```

### 3) Auto-start

Place shortcut in:

`%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup`

## Environment tuning

```bash
REMEM_SCAN_INTERVAL_MS=2000
REMEM_COMMAND_LIMIT_GB=2
REMEM_GROUP_LIMIT_GB=6
REMEM_MAX_LOG_LINES=400
REMEM_EXTRA_COMMANDS="cmd1,cmd2"
```
