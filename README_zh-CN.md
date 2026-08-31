<div align="center">

# MIDIBridge Client

从 MIDIBridge Server 接收 MIDI 信号，注入虚拟 MIDI 端口供其他软件使用

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

## 快速开始

### 环境要求

运行预编译二进制无任何依赖，下载即用。仅从源码构建时需要 Go ≥ 1.26（见[从源码构建](#从源码构建)）。

> [!IMPORTANT]
> Windows 的 MM API 不支持程序化创建虚拟 MIDI 端口<br>
> 请先安装 [loopMIDI](https://www.tobias-erichsen.de/software/loopmidi.html) 并创建一个虚拟端口，名称需与配置中的 `midi.virtualPortName` 一致


从 [Releases](https://github.com/MarchSnow-1/midibridge-client/releases) 下载对应平台的二进制，解压后直接运行：

```bash
./midibridge-client
```

> 首次运行会在可执行文件所在目录下自动生成 `data/config.json`，编辑该文件填入服务端 IP 与密码即可。

## 配置文件

文件位置：可执行文件所在目录下的 `data/config.json`。配置路径锚定到可执行文件自身的位置而非当前工作目录，无论从哪个目录启动，客户端读写的都是同一份配置。首次运行自动生成，使用前需编辑。

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

### 常用配置项

**服务端地址与密码：**

```json
"server": { "host": "192.168.1.100", "port": 9001 },
"auth":  { "password": "你的密码" }
```

**日志语言：**

```json
"lang": "zh-CN"
```

设为 `"en"`（默认）或 `"zh-CN"`，控制所有日志输出的语言。也可通过 `MIDIBRIDGE_LANG` 环境变量或 `--lang` CLI 参数覆盖

**虚拟端口名称：**

```json
"midi": { "virtualPortName": "My DAW Bridge" }
```

macOS / Linux 下虚拟端口会自动创建。Windows 下需先在 loopMIDI 中创建同名端口

端口名匹配为子串匹配而非精确匹配：客户端使用第一个名称**包含** `virtualPortName` 的输出端口（`strings.Contains`）
例如配置为 `"Bridge"` 时，可匹配 `"MIDIBridge"` 或 `"My Bridge"` 端口

**断线重连：**

```json
"reconnect": {
  "enabled": true,
  "intervalMs": 3000,
  "maxAttempts": 0
}
```

- `enabled`：断开后是否自动重连
- `intervalMs`：基础重连间隔（实际采用指数退避 + 随机抖动）
- `maxAttempts`：最大重连次数，`0` = 无限

**被踢后重连：**

```json
"midi": { "reconnectOnKick": true }
```

控制被服务端踢出后的行为（如密码被修改、连接被其他客户端顶替）：

- `false`：客户端发送 All Notes Off 后进程直接退出
- `true`（默认）：客户端保持运行，并按 `reconnect` 配置（`enabled` / `intervalMs` / `maxAttempts`）自动重连

**MIDI 详细日志：**

```json
"logging": { "midiVerbose": false }
```

设为 `true` 时会记录每一条收到的 MIDI 事件。活跃的 MIDI 流会产生大量日志，建议仅在排查问题时开启

**TLS（加密连接）：**

```json
"tls": { "enabled": true, "caCert": "" }
```

启用后客户端通过 `wss://` 连接。详细说明与自签证书示例见[安全说明](#安全说明)

> [!NOTE]
> **CC#120 / CC#123 由客户端本地处理** 客户端自行按通道跟踪按下的音符，并在断开、被踢与退出时发送 All Notes Off；从服务端收到的 All Sound Off（`CC#120`）与 All Notes Off（`CC#123`）消息会被静默过滤，不会转发到虚拟端口

### 配置优先级

数值高的覆盖低的：

1. CLI 参数（最高）
2. 环境变量
3. `data/config.json`
4. 内置默认值（最低）

**CLI 参数：**

```bash
./midibridge-client --host 192.168.1.100 --port 9001 --password 你的密码 --port-name "你的端口名" --lang zh-CN
```

`--version` / `-v` 打印客户端版本号并退出。

> [!WARNING]
> 通过 `--password` 传递密码会暴露在进程列表与 shell 历史中，建议使用 `data/config.json`。详见[安全说明](#安全说明)。

**环境变量：**

| 变量 | 对应配置项 |
|------|------|
| `MIDIBRIDGE_LANG` | `lang` |
| `MIDIBRIDGE_HOST` | `server.host` |
| `MIDIBRIDGE_PORT` | `server.port` |
| `MIDIBRIDGE_PASSWORD` | `auth.password` |
| `MIDIBRIDGE_PORT_NAME` | `midi.virtualPortName` |

## 安全说明

在非完全可信的网络中运行客户端之前，请先阅读本节

- **连接默认为明文** 未启用 TLS 时，客户端通过 `ws://` 连接，所有数据（包括密码）均以明文形式在网络上传输；请仅在可信网络中使用默认模式，或启用 TLS
- **密码以明文存储** 在 `data/config.json` 中（文件以 `0600` 权限创建，仅文件所有者可读）；未启用 TLS 时，密码在认证过程中同样以明文传输
- **优先使用配置文件而非 `--password`** 命令行参数在进程列表与 shell 历史中可见，客户端检测到 `--password` 时会打印安全警告

### 启用 TLS

将 `tls.enabled` 设为 `true`，即可改用 `wss://` 加密连接：

```json
"tls": {
  "enabled": true,
  "caCert": ""
}
```

- `enabled`：通过 `wss://` 连接（TLS ≥ 1.2），服务端证书默认依据系统 CA 证书库校验
- `caCert`：可选的 PEM 证书文件路径。设置后将**取代**系统 CA 库作为校验信任根 —— 用于服务端使用自签证书的场景

**示例 —— 信任自签的服务端证书：**

```json
"tls": {
  "enabled": true,
  "caCert": "ca.crt"
}
```

将服务端证书（或为其签名的 CA 证书）放到指定路径，客户端即可在加密的 `wss://` 连接上完成证书校验

## 从源码构建

### 环境要求

| 依赖 | 说明 |
|------|------|
| Go | ≥ 1.26 |
| GCC | CGO 编译 RtMidi 所需 |

### 构建与运行

Windows

```bash
# 获取源代码
git clone https://github.com/MarchSnow-1/midibridge-client.git
cd midibridge-client

# 拉取依赖
go mod tidy

# 编译
go build -o dist/midibridge-client.exe ./src/

# 运行
./dist/midibridge-client.exe
```

Linux / macOS

```bash
# 获取源代码
git clone https://github.com/MarchSnow-1/midibridge-client.git
cd midibridge-client

# 拉取依赖
go mod tidy

# 编译
go build -o dist/midibridge-client ./src/

# 运行
./dist/midibridge-client
```

## 许可证

[MIT](LICENSE) — 自由使用、修改、分发
