# MIDIBridge IPC — 示例客户端

[English](README.md) | [简体中文](README_zh-CN.md)

本目录是 [MIDIBridge client 本地 IPC 协议](../PROTOCOL_zh-CN.md) 的可用消费端实现，覆盖下列语言，每份示例都会连接 client、读取握手帧，然后把收到的每条 MIDI 消息以空格分隔的大写十六进制打印出来

本目录中的每份示例都已在 Windows 11（amd64）上针对参考实现编译并运行

## 前置条件

client 必须以启用 IPC 的方式运行：

```
midibridge-client -ipc-listen 127.0.0.1:9003
```

所有示例默认连接 `127.0.0.1:9003`，改过端口时在命令行上传入不同地址

## 索引

| 语言 | 文件 | 实测工具链 | 说明 |
| --- | --- | --- | --- |
| C# | [`csharp/Program.cs`](csharp/Program.cs) | .NET 10.0.403 | 需要同目录的 `.csproj` |
| Node.js | [`nodejs/client.js`](nodejs/client.js) | Node v22.23.2 | 内置 `net` 模块，无第三方包 |
| Go | [`go/main.go`](go/main.go) | Go 1.27.3 | 仅标准库 |
| C++ | [`cpp/main.cpp`](cpp/main.cpp) | zig c++ 0.13.0（clang）/ MSVC 19.50 | Windows 上需 `-lws2_32` |
| Python | [`python/client.py`](python/client.py) | Python 3.12.10 | 仅标准库 |
| Java | [`java/MidiBridgeClient.java`](java/MidiBridgeClient.java) | JDK 25.0.3 | 仅需 JDK，无依赖 |
| Rust | [`rust/main.rs`](rust/main.rs) | rustc 1.97.1 | 直接用 `rustc`，无需 Cargo |
| TypeScript | [`typescript/client.ts`](typescript/client.ts) | tsc 5.x | 类型声明写在文件内，无需 `@types/node` |

## 构建与运行

以下每条命令在撰写本文档时都实际执行过

### C#

```bash
cd csharp
dotnet run
```

传入不同地址：`dotnet run -- 127.0.0.1 9004`。工程目标框架为 `net10.0`；若 SDK 较旧，
把 `ipc-consumer.csproj` 中的 `TargetFramework` 改为 `net6.0` 或更高版本即可

### Node.js

```bash
node nodejs/client.js
```

或显式指定地址：`node nodejs/client.js 127.0.0.1 9004`

### TypeScript

该示例编译后的运行行为与 Node.js 版一致，所需类型声明写在文件内部，因此无需 `@types/node`：

```bash
npx tsc typescript/client.ts --outDir dist --strict --target ES2020 --module commonjs
node dist/client.js
```

或显式指定地址：`node dist/client.js 127.0.0.1 9004`

### Go

```bash
cd go
go run main.go
```

`main.go` 只导入标准库，因此在任意目录下 `go run` 都可运行，要构建二进制可用 `go build -o consumer main.go`

与其他示例不同，Go 的地址是一个 `host:port` 参数而不是分开的两个：`go run main.go 127.0.0.1:9004`

### C++

示例是符合标准的 C++17 代码，任何合规编译器都能构建，Windows 上必须带 `-lws2_32`，因为示例使用 Winsock2，不加会报 `WSAStartup`、`closesocket` 等符号未解析

Windows + MinGW g++：

```bash
g++ cpp/main.cpp -lws2_32 -o consumer.exe
./consumer.exe
```

Windows + zig（无需安装 MinGW，本示例即用此方式验证）：

```bash
zig c++ cpp/main.cpp -lws2_32 -std=c++17 -o consumer.exe
./consumer.exe
```

Windows + MSVC（必须加 `/utf-8`：源码带 UTF-8 中文注释，在 CJK 代码页下不加该参数会报 C4819 并触发大量语法错误）：

```bat
cl /utf-8 cpp\main.cpp ws2_32.lib
consumer.exe
```

POSIX（Linux、macOS）——无需额外参数：

```bash
g++ cpp/main.cpp -o consumer
./consumer
```

### Python

```bash
python python/client.py
```

需要 Python 3.6 及以上（用到了 f-string 与 `bytes.hex(sep)`）。无需安装任何包

### Java

```bash
cd java
javac MidiBridgeClient.java
java MidiBridgeClient
```

public 类名必须与文件名一致，因此文件名为 `MidiBridgeClient.java`

### Rust

```bash
cd rust
rustc -O main.rs -o consumer
./consumer
```

无需 `Cargo.toml`：示例没有任何依赖，直接用 `rustc` 即可

## 预期输出

同一条流下，各示例的输出完全一致：

```
connected to 127.0.0.1:9003
control: {"type":"hello","proto":"1.0.0","client":"midibridge-client","version":"1.0.0"}
midi   : 90 3C 64
midi   : 90 40 7F
midi   : B0 07 50
midi   : 80 3C 00
midi   : 90 3C 64
midi   : 80 3C 00
midi   : 90 3E 64
midi   : 80 3E 00
midi   : F0 7E 7F 06 01 F7
read: connection closed
```

最后一行的具体措辞各语言略有不同，且控制帧中 JSON 的键顺序没有保证，请按对象解析而不是做文本匹配

## 这些示例刻意不做什么

它们是用于说明帧处理规则的最小可读参考，不包含自动重连、多路复用、落盘缓冲，也不解析 MIDI 语义，但它们**正确处理**了两件事，因为这两处正是帧处理实现最常出错的地方：

- **部分读取与粘包** 每份示例都按长度前缀承诺的字节数精确读取，在语言 API 需要时跨多次读取累积
- **超长长度拒绝** 长度为 `0` 或超过 1 MiB 时直接关闭连接，而不是尝试分配

注意这条流按设计是有损的，跟不上速度的消费者会静默丢帧，详见[协议第 5 节](../PROTOCOL_zh-CN.md#5-语义与失败模式)
