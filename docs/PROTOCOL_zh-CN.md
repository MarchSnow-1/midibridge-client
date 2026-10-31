# MIDIBridge Client — 本地 IPC 协议

[English](PROTOCOL.md) | [简体中文](PROTOCOL_zh-CN.md)

协议版本 **1.0.0**

协议版本由握手帧的 `proto` 字段携带，client 自身的构建版本由 `version` 字段单独报告

本文档规定 `midibridge-client` 对外提供的本地 TCP 接口

此接口使其他程序可以直接消费实时 MIDI 流，而不必先经过虚拟 MIDI 端口

---

## 1. 概述与适用范围

`midibridge-client` 通常把收到的 MIDI 注入虚拟 MIDI 端口，再由 DAW 或其他 MIDI 软件把它当作输入设备打开

这种方式工作良好，但需要虚拟端口驱动，在 Windows 上还需要用户手动创建一个同名端口

本地 IPC 接口是一个**可选的其他输出**：client 收到的同一条 MIDI 消息，额外发布到一个 TCP socket 上，接入的进程可以按原始字节读取

**该功能默认关闭**：不传 `-ipc-listen` 时，client 的行为与引入该功能之前一致，不打开任何 socket，也不做额外工作

适用场景：

- 供第三方程序消费 MIDI 事件
- 在独立进程中录制或分析这条流

不适用的场景：

- 需要保证不丢帧的场合：这是一条实时的、可能丢帧的流，跟不上速度的消费者会静默丢帧（见第 6 节），需要精确重放时请勿使用

## 2. 地址与端口

监听地址需手动指定，client 不监听任何地址除非显式配置

```
midibridge-client -ipc-listen 127.0.0.1:9003
```

| 形式 | 行为 |
| --- | --- |
| 不设置 / 空 | 关闭 IPC（默认） |
| `127.0.0.1:9003` | 监听指定的回环端口，仅本机可访问 |
| `localhost:9003` | 接受，解析到回环接口 |
| `[::1]:9003` | 接受，IPv6 回环，方括号不可省略 |
| `127.0.0.1:0` | 由系统分配一个空闲端口 |
| `0.0.0.0:9003` | 接受，监听所有接口，见第 8 节安全说明 |
| `:9003` | 接受，等价于监听所有接口 |
| `192.168.1.10:9003` | 接受，仅监听该网卡地址 |
| 缺少端口，如 `127.0.0.1` | 被拒绝，地址格式不完整 |

由于本接口没有鉴权，绑定非回环地址时 client 会记录一条警告，并在监听日志中标注出来

端口由系统分配时，实际地址会在启动时打印到 client 日志：

```
IPC listening on 127.0.0.1:53124 (loopback only, read-only)
```

绑定非回环地址时，同一行会标注为对网络可见：

```
IPC listening on [::]:53124 (reachable from network, NOT authenticated, read-only)
```

需要知道端口的消费者可以解析这一行，或改用固定端口

配置也可以来自 `data/config.json` 的 `ipc.listen`，或环境变量 `MIDIBRIDGE_IPC_LISTEN`，优先级为 文件 < 环境变量 < 命令行，与 client 其余配置一致

## 3. 帧格式

每条消息都被包裹在一帧中：

```
+----------------+------------+-------------------+
| length (4 B)   | kind (1 B) | body (length-1 B) |
| uint32 LE      | uint8      | 不透明载荷        |
+----------------+------------+-------------------+
```

- `length` 是 32 位无符号小端整数，统计 `kind` 字节加 body 的长度，不包含 4 字节 length 自身
- `kind` 标识帧类型，见第 4 节
- 单帧上限为 1 MiB（1 048 576 字节），消费者遇到更大的 `length` 应当拒绝并关闭连接，而不是尝试分配内存，这个上限对最大 64 KiB 的 SysEx 载荷留有充裕余量

使用长度前缀是因为 MIDI 是二进制协议，载荷字节可以是任意值，包括 `0x0A`，因此按行分隔的帧格式无法工作

同样因为长度前缀，消费者不能假设一次 `read()` 就返回完整的一帧，各语言的处理方式见第 8 节

## 4. 帧类型与握手

| `kind` | 名称 | body 内容 |
| --- | --- | --- |
| `0x01` | `CONTROL_JSON` | UTF-8 编码的 JSON 对象 |
| `0x02` | `MIDI` | 一条完整 MIDI 消息 |
| 其他 | 保留 | 消费者应当跳过未知类型并继续读取 |

### 握手

连接建立后，服务端立即发送一帧 `CONTROL_JSON`：

```json
{"type":"hello","proto":"1.0.0","client":"midibridge-client","version":"1.0.0"}
```

| 字段 | 含义 |
| --- | --- |
| `type` | 该帧恒为 `"hello"` |
| `proto` | 协议版本，语义化版本字符串，当前为 `"1.0.0"` |
| `client` | 恒为 `"midibridge-client"` |
| `version` | 客户端构建版本，仅供参考 |

hello 帧之后，只要连接保持，服务端就只发送 `MIDI` 帧，每条 MIDI 消息一帧

### MIDI 帧的 body

body 是从服务端收到的原始 MIDI 消息字节，不做重新编码：

| 消息 | 字节 |
| --- | --- |
| Note On，通道 1，C4，力度 100 | `90 3C 64` |
| Note Off，通道 1，C4 | `80 3C 00` |
| Control Change，通道 1，CC#7，值 80 | `B0 07 50` |
| SysEx（变长） | `F0 7E 7F 06 01 F7` |

需要音名、通道号或控制器语义的消费者必须自行解析 MIDI 字节，协议不解释这些语义

## 5. 语义与失败模式

**顺序**：帧按 client 收到的顺序投递，TCP 保证该顺序

**无时间戳**：每帧只携带 MIDI 字节，需要计时的消费者在帧到达时用自己的时钟记录

client 不附带服务端的 delta time，因为本地投递耗时极短，消费者侧的到达时刻才是下游真正关心的

**独立于虚拟端口节流**：client 为虚拟 MIDI 端口写入做了节流，避免撑爆驱动缓冲区，该节流最多插入 100 ms 延迟

IPC 流在节流之前发布，因此 IPC 消费者看到的是消息到达时的状态，不会排在 MIDI 端口写入之后

**慢消费者丢帧，不阻塞生产端**：每个消费者有独立的发送队列，深度为 2048 帧

队列满时丢弃最新的那一帧，队列中已有的帧保持不动

`Publish` 调用无论队列是否已满都立即返回，MIDI 热路径因此不会被单个慢消费者拖住

丢帧只能在 client 日志里观察到，第一帧被丢弃时记录一次，此后每丢弃 500 帧再记录一次，每条都带累计值：

```
IPC consumer is slow, dropped 1 frame(s) so far
IPC consumer is slow, dropped 501 frame(s) so far
IPC consumer is slow, dropped 1001 frame(s) so far
```

这个代价是刻意的，是「不阻塞 MIDI 热路径」这一设计的结果，不是缺陷

由此可以推出以下几点，消费者需要理解：

- **尽快消费**：停顿或处理过慢的消费者会丢帧
- **协议层没有负反馈机制**：被丢弃的帧不会产生任何通知，消费者收不到提示
- **协议不对帧编号**：消费者无法从数据本身发现空洞
- 若消费者不能容忍丢帧，需要自行保证消费速度，或在应用层加序号与校验，本接口不提供投递保证

**连接上限**：最多 8 个并发消费者，超出时新连接被拒绝，断开后槽位随即释放

**只读**：协议版本 1 中服务端不读取消费者 socket，消费者写入的内容不影响这条流

**无鉴权**：见第 7 节

## 6. 版本与向后兼容

hello 帧中的 `proto` 字段是兼容性契约，本协议版本号遵循语义化版本进行迭代，同主版本号内的协议向下兼容

- 消费者应当读取 hello 帧并在假设字段布局前检查 `proto`
- 遇到不理解的 `proto` 值时，消费者可以告警后继续读取，未知帧类型是跳过而非致命
- 帧类型 `0x03` 及以上保留给未来使用，新增帧类型属于兼容变更，改变帧头布局则不属于
- 当前没有协商阶段，服务端声明自己的版本后会直接开始传输

`docs/examples/` 下的示例会打印 `proto`，但不拒绝未知值

生产环境的消费者应当据此自行处理版本判断

## 7. 安全说明

该接口不加密、无鉴权，其影响取决于绑定的地址

- **能访问到端口的进程都能连接**：没有密码，端口可被访问时，对应范围内的任意进程都能读取这条 MIDI 流
- **回环绑定**：绑定 `127.0.0.1`、`localhost` 或 `::1` 时，只有本机进程能连接，流量不经过网络接口
- **非回环绑定**：绑定 `0.0.0.0`、`:PORT` 或某个网卡地址时，能路由到该地址的机器都能读取这条流，此时 client 会在启动日志中记录警告并标注 `reachable from network, NOT authenticated`
- **无传输加密**：回环绑定下流量不出本机，加 TLS 只增加开销，因此需要机密性时请在下游自行处理
- **该流是只读的**：任何接入方都无法通过此接口向 client 注入 MIDI

## 8. 各语言示例

每份示例都是自包含的单个文件：连接 client、打印握手帧，随后把每条 MIDI 消息以空格分隔的大写十六进制打印出来，复制出来即可运行，都不依赖第三方包

各语言的构建与运行方法以及它们实际产生的输出，见 [`examples/README_zh-CN.md`](examples/README_zh-CN.md)

| 语言 | 文件 | 工具链 |
| --- | --- | --- |
| C# | [`examples/csharp/Program.cs`](examples/csharp/Program.cs) | .NET 6+，含工程文件 |
| Node.js | [`examples/nodejs/client.js`](examples/nodejs/client.js) | 内置 `net` 模块 |
| Go | [`examples/go/main.go`](examples/go/main.go) | 仅标准库 |
| C++ | [`examples/cpp/main.cpp`](examples/cpp/main.cpp) | C++17，Windows 上需 `-lws2_32` |
| Python | [`examples/python/client.py`](examples/python/client.py) | 仅标准库 |
| Java | [`examples/java/MidiBridgeClient.java`](examples/java/MidiBridgeClient.java) | 仅需 JDK |
| Rust | [`examples/rust/main.rs`](examples/rust/main.rs) | 直接用 `rustc`，无需 Cargo |

每份示例都处理了帧实现最容易出错的两点：部分读取与粘包，即严格按长度前缀承诺的字节数读取；以及超长长度拒绝，即长度为 `0` 或超过 1 MiB 时直接关闭连接而不尝试分配

所有示例都已在参考实现上编译并运行通过

## 9. 参考输出

同一条流下，各示例的输出等价，面对一个故意拆分与合并帧的测试服务端，输出为：

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

`version` 的值是 client 构建时注入的版本号，正式发布为 tag 值，dev 流水线为 `git describe` 推导值，源码直接 `go build` 未注入时为 `dev`

上面的控制帧按 client 实际发送的字段顺序原样列出，把控制帧解码成对象的示例可能以不同顺序打印键，JSON 键顺序没有保证，请按对象解析控制帧，不要做文本匹配

最后一行的措辞各语言不同，Go 与 Rust 内部把对端关闭报告为 `EOF` 与 `UnexpectedEndOfFile` 再翻译成 `connection closed`，其余语言直接输出该文本

部分示例的 `midi` 行前缀会补空格以对齐列

## 10. 故障排查

**`connection refused` / `connect failed`**

client 没有在监听，确认它以 `-ipc-listen` 启动且地址匹配，若端口设为 `:0`，从 client 日志的 `IPC listening on ...` 那一行读取实际端口

**client 日志报 `address already in use`**

端口被其他进程占用，更换端口或使用 `127.0.0.1:0` 来自动分配端口

**client 拒绝以所配地址启动**

该地址格式不完整，最常见的原因是缺少端口，例如只写了 `127.0.0.1` 而没有 `:9003`

**日志提示监听地址不是回环**

绑定了非回环地址，这条 MIDI 流可被网络访问且没有鉴权，可通过主机防火墙限制访问或改为只绑定本机回环地址

**连上了但只收到 hello 帧**

没有 MIDI 数据在流动，检查 `midibridge-server` 是否运行、client 是否认证成功（日志行 `Authenticated, receiving MIDI signals`），以及是否有信号输入到服务端的 MIDI 输入端

**负载高时消息停止到达**

该消费者的队列溢出，其消息被丢弃，client 会记录 `IPC consumer is slow, dropped N frame(s) so far`，首次丢弃时记录一次，此后每满 500 次再记一次

这是刻意设计，以保证单个慢消费者不会拖住整条 MIDI 流，请加快消费速度、降低消息速率，或接受丢帧，注意协议传输的帧不带序号，消费者无法从数据本身判断是否发生了丢帧

**输出看起来是乱码，或程序读出巨大的长度值**

说明正从帧的中间位置开始读取，原因通常是代码把部分读取当成了完整帧，请使用对应语言示例中的「读满 N 字节」循环

**第 9 个消费者连不上**

最多支持 8 个并发消费者，请关闭一个未使用的连接
