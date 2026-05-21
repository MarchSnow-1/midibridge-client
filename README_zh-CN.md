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

| 依赖 | 说明 |
|------|------|
| Go | ≥ 1.23 |


> [!IMPORTANT]
> Windows 的 MM API 不支持程序化创建虚拟 MIDI 端口<br>
> 请先安装 [loopMIDI](https://www.tobias-erichsen.de/software/loopmidi.html) 并创建一个虚拟端口，名称需与配置中的 `midi.virtualPortName` 一致


从 [Releases](https://github.com/MarchSnow-1/midibridge-client/releases) 下载对应平台的二进制，解压后直接运行：

```bash
./midibridge-client
```

> 首次运行会自动生成 `data/config.json`，编辑该文件填入服务端 IP 与密码即可。

## 配置文件

文件位置：`data/config.json`，首次运行自动生成，使用前需编辑。

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

设为 `"en"`（默认）或 `"zh-CN"`，控制所有日志输出的语言。也可通过 `MIDIBRIDGE_LANG` 环境变量或 `--lang` CLI 参数覆盖。

**虚拟端口名称：**

```json
"midi": { "virtualPortName": "My DAW Bridge" }
```

macOS / Linux 下虚拟端口会自动创建。Windows 下需先在 loopMIDI 中创建同名端口。

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

设为 `false` 时，被服务端踢出（如密码被修改）后客户端直接退出。设为 `true` 时仅打印警告，需手动更新密码后重启。

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

**环境变量：**

| 变量 | 对应配置项 |
|------|------|
| `MIDIBRIDGE_LANG` | `lang` |
| `MIDIBRIDGE_HOST` | `server.host` |
| `MIDIBRIDGE_PORT` | `server.port` |
| `MIDIBRIDGE_PASSWORD` | `auth.password` |
| `MIDIBRIDGE_PORT_NAME` | `midi.virtualPortName` |

## 从源码构建

### 环境要求

| 依赖 | 说明 |
|------|------|
| Go | ≥ 1.22 |
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
