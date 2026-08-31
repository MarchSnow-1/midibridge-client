<div align="center">

# MIDIBridge Client

Receive MIDI signals from MIDIBridge Server over the network and inject them into a virtual MIDI port for use by other software.

<!-- Badges -->

[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-blue?style=for-the-badge)](https://github.com/MarchSnow-1/midibridge-client)
[![Golang](https://img.shields.io/badge/Golang-1.26%2B-green?style=for-the-badge)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-orange?style=for-the-badge)](LICENSE)
<br>
[![GitHub Release](https://img.shields.io/github/v/release/MarchSnow-1/midibridge-client?style=for-the-badge)](https://github.com/MarchSnow-1/midibridge-client/releases)
[![GitHub Repo stars](https://img.shields.io/github/stars/MarchSnow-1/midibridge-client?style=for-the-badge)](https://github.com/MarchSnow-1/midibridge-client)
[![GitHub Last Commit](https://img.shields.io/github/last-commit/MarchSnow-1/midibridge-client?style=for-the-badge)](https://github.com/MarchSnow-1/midibridge-client)
[![Total Download](https://img.shields.io/github/downloads/MarchSnow-1/midibridge-client/total?style=for-the-badge)](https://github.com/MarchSnow-1/midibridge-client/releases)

[**English**](README.md) | [**简体中文**](README_zh-CN.md)

</div>

## Quick Start

### Requirements

None for the prebuilt binary — just download and run. Go ≥ 1.26 is only needed if you [build from source](#build-from-source).

> [!IMPORTANT]
> Windows MM API does not support programmatic virtual MIDI port creation.<br>
> Install [loopMIDI](https://www.tobias-erichsen.de/software/loopmidi.html) first and create a virtual port — the name must match `midi.virtualPortName` in your config.


Download the binary for your platform from [Releases](https://github.com/MarchSnow-1/midibridge-client/releases), extract and run:

```bash
./midibridge-client
```

> The client auto-generates `data/config.json` next to the executable on first run. Edit it to set your server IP and password.

## Configuration

File: `data/config.json` inside the directory containing the executable. The path is anchored to the executable's own location — not the current working directory — so the client always reads and writes the same config no matter where it is launched from. Auto-generated on first run. Edit before starting.

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
    "file": false,
    "midiVerbose": false
  },
  "tls": {
    "enabled": false,
    "caCert": ""
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

Port name matching is substring-based, not exact: the client picks the **first** output port whose name *contains* the configured `virtualPortName` (`strings.Contains`). For example, a configured name of `"Bridge"` matches ports like `"MIDIBridge"` or `"My Bridge"`.

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

Controls what happens after being kicked by the server (e.g. the password was changed, or the connection was taken over by another client):

- `false`: the client sends All Notes Off, then the process exits.
- `true` (default): the client keeps running and reconnects automatically according to the `reconnect` settings (`enabled`, `intervalMs`, `maxAttempts`).

**Verbose MIDI logging:**

```json
"logging": { "midiVerbose": false }
```

When `true`, every incoming MIDI event is logged at INFO level. Active MIDI streams produce a lot of log output — enable only when debugging.

**TLS (encrypted connection):**

```json
"tls": { "enabled": true, "caCert": "" }
```

When `enabled`, the client connects via `wss://`. See [Security](#security) for details and a self-signed certificate example.

> [!NOTE]
> **CC#120 / CC#123 are handled locally.** The client tracks held notes per channel and sends All Notes Off itself (on disconnect, kick, and shutdown). All Sound Off (`CC#120`) and All Notes Off (`CC#123`) messages received from the server are silently filtered and never forwarded to the virtual port.

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

`--version` / `-v` prints the client version and exits.

> [!WARNING]
> Passing the password via `--password` exposes it in the process list and shell history. Prefer `data/config.json` — see [Security](#security).

**Environment variables:**

| Variable | Maps to |
|----------|---------|
| `MIDIBRIDGE_LANG` | `lang` |
| `MIDIBRIDGE_HOST` | `server.host` |
| `MIDIBRIDGE_PORT` | `server.port` |
| `MIDIBRIDGE_PASSWORD` | `auth.password` |
| `MIDIBRIDGE_PORT_NAME` | `midi.virtualPortName` |

## Security

Read this section before running the client outside a network you fully trust.

- **The connection is plaintext by default.** Unless TLS is enabled, the client connects over `ws://`, and everything — including the password — travels unencrypted over the network. Use the default mode only on trusted networks, or enable TLS.
- **The password is stored in plaintext** in `data/config.json` (created with `0600` permissions — readable by the file owner only) and, without TLS, it is also transmitted in cleartext during authentication.
- **Prefer the config file over `--password`.** Command-line arguments are visible in the process list and shell history. The client prints a security warning whenever `--password` is used.

### Enabling TLS

Set `tls.enabled` to `true` to connect over `wss://` instead:

```json
"tls": {
  "enabled": true,
  "caCert": ""
}
```

- `enabled`: connect via `wss://` (TLS ≥ 1.2). The server certificate is verified against the system CA store.
- `caCert`: optional path to a PEM certificate file. When set, it **replaces** the system CA store as the trust root for verification — intended for servers that use self-signed certificates.

**Example — trusting a self-signed server certificate:**

```json
"tls": {
  "enabled": true,
  "caCert": "ca.crt"
}
```

Put the server's certificate (or the CA certificate that signed it) at the configured path, and the client will verify the server against it over an encrypted `wss://` connection.

## Build from Source

### Requirements

| Dependency | Notes |
|------------|-------|
| Go | ≥ 1.26 |
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
