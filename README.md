<div align="center">

# MIDIBridge Client

Receive MIDI signals from MIDIBridge Server over the network and inject them into a virtual MIDI port for use by other software.

> Repository: `github.com/MarchSnow-1/midibridge-client`

[**English**](README.md) | [**简体中文**](README_zh-CN.md)

</div>

## Quick Start

### Requirements

| Dependency | Notes |
|------------|-------|
| Go | ≥ 1.23 |


> [!IMPORTANT]
> Windows MM API does not support programmatic virtual MIDI port creation.<br>
> Install [loopMIDI](https://www.tobias-erichsen.de/software/loopmidi.html) first and create a virtual port — the name must match `midi.virtualPortName` in your config.


Download the binary for your platform from [Releases](https://github.com/MarchSnow-1/midibridge-client/releases), extract and run:

```bash
./midibridge-client
```

> The client auto-generates `data/config.json` on first run. Edit it to set your server IP and password.

## Configuration

File: `data/config.json`. Auto-generated on first run. Edit before starting.

```json
{
  "lang": "en",
  "server": {
    "host": "192.168.1.100",
    "port": 9001
  },
  "auth": {
    "password": ""
  },
  "midi": {
    "virtualPortName": "MIDIBridge",
    "reconnectOnKick": true
  },
  "reconnect": {
    "enabled": true,
    "intervalMs": 3000,
    "maxAttempts": 0
  },
  "logging": {
    "file": false
  }
}
```

### Common Settings

**Server address & password:**

```json
"server": { "host": "192.168.1.100", "port": 9001 },
"auth":  { "password": "your_password" }
```

**Log language:**

```json
"lang": "zh-CN"
```

Set to `"en"` (default) or `"zh-CN"`. Controls the language of all log output. Can also be set via `MIDIBRIDGE_LANG` env variable or `--lang` CLI argument.

**Virtual port name:**

```json
"midi": { "virtualPortName": "My DAW Bridge" }
```

On macOS / Linux the port is created automatically. On Windows, create a port with the same name in loopMIDI first.

**Reconnect behavior:**

```json
"reconnect": {
  "enabled": true,
  "intervalMs": 3000,
  "maxAttempts": 0
}
```

- `enabled`: whether to auto-reconnect on disconnect
- `intervalMs`: base reconnect interval (exponential backoff with jitter applied)
- `maxAttempts`: max reconnect attempts, `0` = unlimited

**Reconnect on kick:**

```json
"midi": { "reconnectOnKick": true }
```

When `false`, the client exits after being kicked (e.g. after a password change). When `true`, it logs a warning and stays idle — update your password and restart.

### Configuration Priority

Higher priority overrides lower:

1. CLI arguments (highest)
2. Environment variables
3. `data/config.json`
4. Built-in defaults (lowest)

**CLI arguments:**

```bash
./midibridge-client --host 192.168.1.100 --port 9001 --password mypass --port-name "My Bridge" --lang zh-CN
```

**Environment variables:**

| Variable | Maps to |
|----------|---------|
| `MIDIBRIDGE_LANG` | `lang` |
| `MIDIBRIDGE_HOST` | `server.host` |
| `MIDIBRIDGE_PORT` | `server.port` |
| `MIDIBRIDGE_PASSWORD` | `auth.password` |
| `MIDIBRIDGE_PORT_NAME` | `midi.virtualPortName` |

## Build from Source

### Requirements

| Dependency | Notes |
|------------|-------|
| Go | ≥ 1.22 |
| GCC | Required for CGO (RtMidi linking) |

### Build & Run

Windows

```bash
# Clone the repo
git clone https://github.com/MarchSnow-1/midibridge-client.git
cd midibridge-client

# Pull dependencies
go mod tidy

# Build
go build -o dist/midibridge-client.exe ./src/

# Run
./dist/midibridge-client.exe
```

Linux / macOS

```bash
# Clone the repo
git clone https://github.com/MarchSnow-1/midibridge-client.git
cd midibridge-client

# Pull dependencies
go mod tidy

# Build
go build -o dist/midibridge-client ./src/

# Run
./dist/midibridge-client
```

## License

[MIT](LICENSE) — Use, modify, and distribute freely.
