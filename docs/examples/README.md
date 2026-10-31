# MIDIBridge IPC — Example clients

[English](README.md) | [简体中文](README_zh-CN.md)

Working consumers for the [MIDIBridge client local IPC protocol](../PROTOCOL.md), covering the languages below, and each one connects to the client, reads the handshake frame, then prints every MIDI message it receives as space-separated uppercase hex

Every example in this folder has been compiled and run against the reference implementation on Windows 11 (amd64), and the commands below are the exact ones used

## Prerequisites

The client must be running with IPC enabled:

```
midibridge-client -ipc-listen 127.0.0.1:9003
```

All examples default to `127.0.0.1:9003`, so pass a different address on the command line if the port was changed

## Index

| Language | File | Toolchain used | Notes |
| --- | --- | --- | --- |
| C# | [`csharp/Program.cs`](csharp/Program.cs) | .NET 10.0.403 | Needs the `.csproj` beside it |
| Node.js | [`nodejs/client.js`](nodejs/client.js) | Node v22.23.2 | Built-in `net` module, no packages |
| TypeScript | [`typescript/client.ts`](typescript/client.ts) | tsc 5.x | Types declared inline, so no `@types/node` is needed |
| Go | [`go/main.go`](go/main.go) | Go 1.27.3 | Standard library only |
| C++ | [`cpp/main.cpp`](cpp/main.cpp) | zig c++ 0.13.0 (clang) / MSVC 19.50 | Needs `-lws2_32` on Windows |
| Python | [`python/client.py`](python/client.py) | Python 3.12.10 | Standard library only |
| Java | [`java/MidiBridgeClient.java`](java/MidiBridgeClient.java) | JDK 25.0.3 | JDK only, no dependencies |
| Rust | [`rust/main.rs`](rust/main.rs) | rustc 1.97.1 | Plain `rustc`, no Cargo needed |

## Build and run

Every command below was executed in this repository while writing these docs

### C#

```bash
cd csharp
dotnet run
```

Pass a different address with `dotnet run -- 127.0.0.1 9004`, and change `TargetFramework` in `ipc-consumer.csproj` to `net6.0` or later if the SDK is older

### Node.js

```bash
node nodejs/client.js
```

Or with an explicit address: `node nodejs/client.js 127.0.0.1 9004`

### TypeScript

The example compiles to the same runtime behaviour as the Node.js one, and the types it needs are declared inside the file so no `@types/node` is required:

```bash
npx tsc typescript/client.ts --outDir dist --strict --target ES2020 --module commonjs
node dist/client.js
```

Or with an explicit address: `node dist/client.js 127.0.0.1 9004`

### Go

```bash
cd go
go run main.go
```

`main.go` imports only the standard library, so `go run` works from any directory, and a binary can be built with `go build -o consumer main.go`

Unlike the other examples, the address is one `host:port` argument rather than two separate ones: `go run main.go 127.0.0.1:9004`

### C++

The example is standard C++17 and builds with any conforming compiler, while Windows needs `-lws2_32` because the example uses Winsock2, without which `WSAStartup`, `closesocket` and friends stay unresolved

Windows with MinGW g++:

```bash
g++ cpp/main.cpp -lws2_32 -o consumer.exe
./consumer.exe
```

Windows with zig, which needs no MinGW install and is what this example was verified with:

```bash
zig c++ cpp/main.cpp -lws2_32 -std=c++17 -o consumer.exe
./consumer.exe
```

Windows with MSVC, where `/utf-8` is required because the source carries UTF-8 comments and `cl` on a CJK code page otherwise fails with C4819 and a wall of syntax errors:

```bat
cl /utf-8 cpp\main.cpp ws2_32.lib
consumer.exe
```

POSIX, with no extra flag:

```bash
g++ cpp/main.cpp -o consumer
./consumer
```

### Python

```bash
python python/client.py
```

Python 3.6 or later is needed because the script uses f-strings and `bytes.hex(sep)`, and no packages have to be installed

### Java

```bash
cd java
javac MidiBridgeClient.java
java MidiBridgeClient
```

The public class name must match the file name, which is why the file is `MidiBridgeClient.java`

### Rust

```bash
cd rust
rustc -O main.rs -o consumer
./consumer
```

No `Cargo.toml` is needed: the example has no dependencies, so plain `rustc` is enough

## Expected output

All examples produce the same output for the same stream:

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

The exact wording of the final line varies by language, and JSON key order in the control frame is not guaranteed, so parse it as an object instead of matching text

## What these examples deliberately do not do

They are minimal readable references for the framing rules, with no reconnect, multiplexing or on-disk buffering, and they do not interpret MIDI semantics, but they **do** handle the two things that break framing implementations most often:

- **Partial reads and coalesced frames** every example reads exactly the number of bytes the length prefix promises, accumulating across multiple reads where the language API needs it
- **Oversized length rejection** a length of `0` or above 1 MiB closes the connection instead of allocating

Note that the stream is lossy by design, so a consumer that falls behind drops frames silently, see [protocol section 5](../PROTOCOL.md#5-semantics-and-failure-modes)
