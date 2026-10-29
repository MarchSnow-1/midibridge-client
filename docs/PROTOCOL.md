# MIDIBridge Client — Local IPC Protocol

[English](PROTOCOL.md) | [简体中文](PROTOCOL_zh-CN.md)

Protocol version **1.0.0**

The protocol version is carried in the `proto` field of the handshake frame, while the client's own build version is reported separately in the `version` field

This document specifies the local TCP interface that `midibridge-client` exposes

The interface lets other programs consume the live MIDI stream directly, without going through a virtual MIDI port first

---

## 1. Overview and scope

`midibridge-client` normally injects received MIDI into a virtual MIDI port, which a DAW or other MIDI software then opens as an input device

That works well, but it needs a virtual port driver, and on Windows the user has to create a port with a matching name by hand

The local IPC interface is an **optional additional output**: the same MIDI messages the client receives are also published on a TCP socket, where a connecting process can read them as raw bytes

**The feature is off by default**: without `-ipc-listen` the client behaves as it did before the feature existed, opening no socket and doing no extra work

Intended for:

- third-party programs that consume MIDI events
- recording or analysing the stream in a separate process

Not intended for:

- use cases that require guaranteed delivery: this is a real-time stream that can drop frames, and a consumer that cannot keep up drops frames silently (see section 5), so do not use it where exact replay matters

## 2. Address and port

The listen address is specified explicitly; the client listens nowhere unless configured

```
midibridge-client -ipc-listen 127.0.0.1:9003
```

| Form | Behaviour |
| --- | --- |
| unset / empty | IPC disabled (default) |
| `127.0.0.1:9003` | Listen on the given loopback port, reachable from this machine only |
| `localhost:9003` | Accepted, resolves to the loopback interface |
| `[::1]:9003` | Accepted, IPv6 loopback, the brackets are required |
| `127.0.0.1:0` | Let the OS pick a free port |
| `0.0.0.0:9003` | Accepted, listens on all interfaces, see the security section 7 |
| `:9003` | Accepted, shorthand for all interfaces |
| `192.168.1.10:9003` | Accepted, restricts the listener to that interface address |
| missing port, e.g. `127.0.0.1` | Rejected, the address is incomplete |

Because the interface has no authentication, the client logs a warning and marks the listener line when a non-loopback address is bound

When the port is chosen by the OS, the actual address is printed to the client log at startup:

```
IPC listening on 127.0.0.1:53124 (loopback only, read-only)
```

For a non-loopback bind the same line is marked as reachable from the network:

```
IPC listening on [::]:53124 (reachable from network, NOT authenticated, read-only)
```

A consumer that needs the port can parse that line, or use a fixed port instead

The address can also come from `ipc.listen` in `data/config.json` or from the `MIDIBRIDGE_IPC_LISTEN` environment variable; precedence is file < environment < command line, consistent with the rest of the client configuration

## 3. Frame format

Every message is wrapped in a frame:

```
+----------------+------------+-------------------+
| length (4 B)   | kind (1 B) | body (length-1 B) |
| uint32 LE      | uint8      | opaque payload    |
+----------------+------------+-------------------+
```

- `length` is a 32-bit unsigned little-endian integer covering the `kind` byte plus the body, and does not include the 4-byte length field itself
- `kind` identifies the frame type, see section 4
- The maximum frame size is 1 MiB (1 048 576 bytes); a consumer that sees a larger `length` should refuse the frame and close the connection rather than try to allocate, and that limit leaves ample headroom over the 64 KiB maximum SysEx payload

The length prefix exists because MIDI is a binary protocol, so payload bytes take any value including `0x0A`, which makes a line-delimited framing unusable

The same prefix means a consumer cannot assume a single `read()` returns a complete frame; see section 8 for how each language handles it

## 4. Frame types and handshake

| `kind` | Name | Body |
| --- | --- | --- |
| `0x01` | `CONTROL_JSON` | UTF-8 encoded JSON object |
| `0x02` | `MIDI` | One complete MIDI message |
| other | reserved | A consumer should skip unknown types and keep reading |

### Handshake

Once the connection is established the server immediately sends one `CONTROL_JSON` frame:

```json
{"type":"hello","proto":"1.0.0","client":"midibridge-client","version":"1.0.0"}
```

| Field | Meaning |
| --- | --- |
| `type` | `"hello"` in the handshake frame |
| `proto` | Protocol version as a semantic version string, currently `"1.0.0"` |
| `client` | `"midibridge-client"` in the handshake frame |
| `version` | Client build version, informational only |

After the hello frame the server sends only `MIDI` frames, one per MIDI message, for as long as the connection stays open

### MIDI frame body

The body is the raw MIDI message bytes received from the server, with no re-encoding:

| Message | Bytes |
| --- | --- |
| Note On, channel 1, C4, velocity 100 | `90 3C 64` |
| Note Off, channel 1, C4 | `80 3C 00` |
| Control Change, channel 1, CC#7, value 80 | `B0 07 50` |
| SysEx (variable length) | `F0 7E 7F 06 01 F7` |

A consumer that needs note names, channel numbers or controller semantics must parse the MIDI bytes itself; the protocol does not interpret them

## 5. Semantics and failure modes

**Ordering**: frames are delivered in the order the client received them, and TCP preserves that order

**No timestamps**: each frame carries only MIDI bytes, and a consumer that needs timing records it with its own clock on arrival

The client does not attach the server-side delta time, because local delivery takes very little time and the arrival instant on the consumer side is what a downstream stage actually cares about

**Independent of virtual-port pacing**: the client paces writes to the virtual MIDI port to avoid overrunning the driver buffer, and that pacing can insert up to 100 ms of delay

The IPC stream is published before the pacing step, so IPC consumers observe messages as they arrive rather than queued behind the port write

**Slow consumers drop frames, the producer is not blocked**: each consumer has its own send queue with a depth of 2048 frames

When a queue is full the newest frame is dropped and the frames already queued stay untouched

The `Publish` call returns immediately whether or not a queue is full, so a single slow consumer cannot stall the MIDI hot path

Drops are observable only in the client log, which records the first dropped frame and then every 500th after that, each with a running total:

```
IPC consumer is slow, dropped 1 frame(s) so far
IPC consumer is slow, dropped 501 frame(s) so far
IPC consumer is slow, dropped 1001 frame(s) so far
```

The cost is deliberate and follows from the decision not to block the MIDI hot path, it is not a defect

Several consequences follow, and consumers need to understand them:

- **Consume promptly**: a consumer that stalls or handles frames too slowly drops frames
- **No feedback at the protocol layer**: a dropped frame produces no notification, so the consumer receives no signal
- **Frames are not numbered**: a consumer cannot discover a gap from the data itself
- If a consumer cannot tolerate drops it must guarantee its own consumption rate, or add sequence numbers and checks at the application layer; this interface offers no delivery guarantee

**Connection limit**: at most 8 concurrent consumers, further attempts are refused, and a slot is released as soon as a consumer disconnects

**Read-only**: in protocol version 1 the server does not read from a consumer socket, so anything a consumer writes has no effect on the stream

**No authentication**: see section 7

## 6. Versioning and compatibility

The `proto` field in the hello frame is the compatibility contract, and the protocol version follows semantic versioning, so protocol versions sharing a major number are backward compatible

- A consumer should read the hello frame and check `proto` before assuming the field layout
- A consumer that sees a `proto` value it does not understand may warn and keep reading, since unknown frame kinds are skipped rather than fatal
- Frame kinds `0x03` and above are reserved for future use; adding a new kind is a compatible change, changing the frame header layout is not
- There is no negotiation phase: the server states its version and starts transmitting

The examples under `docs/examples/` print `proto` but do not reject unknown values

Production consumers should handle the version check themselves

## 7. Security

The interface is unencrypted and unauthenticated, and the implications depend on the bound address

- **Any process that can reach the port can connect**: there is no password, so when the port is reachable any process within that reach can read the MIDI stream
- **Loopback binding**: with `127.0.0.1`, `localhost` or `::1` only local processes can connect, and traffic does not traverse a network interface
- **Non-loopback binding**: with `0.0.0.0`, `:PORT` or an interface address, any machine that can route to that address can read the stream, and in this case the client logs a warning at startup and marks the listener `reachable from network, NOT authenticated`
- **No transport encryption**: under a loopback bind traffic stays on the machine and TLS would add cost without benefit, so handle confidentiality downstream if you need it
- **The stream is read-only**: nothing that connects can inject MIDI back into the client through this interface

## 8. Language examples

Each example is a self-contained single file: it connects to the client, prints the handshake frame, then prints every MIDI message as space-separated uppercase hex; copy one out and run it, none of them needs a third-party package

Build and run instructions for each language, plus the output they actually produce, are in [`examples/README.md`](examples/README.md)

| Language | File | Toolchain |
| --- | --- | --- |
| C# | [`examples/csharp/Program.cs`](examples/csharp/Program.cs) | .NET 6+, project file included |
| Node.js | [`examples/nodejs/client.js`](examples/nodejs/client.js) | Built-in `net` module |
| Go | [`examples/go/main.go`](examples/go/main.go) | Standard library only |
| C++ | [`examples/cpp/main.cpp`](examples/cpp/main.cpp) | C++17, needs `-lws2_32` on Windows |
| Python | [`examples/python/client.py`](examples/python/client.py) | Standard library only |
| Java | [`examples/java/MidiBridgeClient.java`](examples/java/MidiBridgeClient.java) | JDK only |
| Rust | [`examples/rust/main.rs`](examples/rust/main.rs) | Plain `rustc`, no Cargo needed |

Every example handles the two things that break framing implementations most often: partial reads and coalesced frames, that is it reads exactly the number of bytes the length prefix promises; and oversized length rejection, that is a length of `0` or above 1 MiB closes the connection instead of allocating

All of them have been compiled and run against the reference implementation

## 9. Reference output

All examples produce equivalent output for the same stream; against a test server that splits and coalesces frames deliberately the output is:

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

The `version` value is the client build version, so a development build prints `dev` rather than a release number

The control frame above is listed in the field order the client actually emits; an example that decodes it into an object may print the keys in a different order, and JSON key order is not guaranteed, so parse the control frame as an object rather than matching text

The wording of the final line differs by language: Go and Rust report a peer close internally as `EOF` and `UnexpectedEndOfFile` and translate it to `connection closed`, while the others print that text directly

Some examples pad the `midi` prefix so the columns line up

## 10. Troubleshooting

**`connection refused` / `connect failed`**

The client is not listening; confirm it was started with `-ipc-listen` and that the address matches, and if the port was set to `:0` read the actual port from the `IPC listening on ...` line in the client log

**`address already in use` in the client log**

Another process holds the port; change the port or use `127.0.0.1:0` to have the OS assign one

**The client refuses to start with the configured address**

The address is incomplete, and the most common cause is a missing port such as `127.0.0.1` without `:9003`

**The log says the listen address is not loopback**

A non-loopback address was bound, so the stream is reachable from the network without authentication; restrict access with a host firewall or bind the loopback address instead

**Connected but only the hello frame arrives**

No MIDI is flowing; check that `midibridge-server` is running, that the client authenticated successfully (log line `Authenticated, receiving MIDI signals`), and that something is playing into the server's MIDI input

**Messages stop arriving under load**

The consumer's queue overflowed and its messages were dropped; the client logs `IPC consumer is slow, dropped N frame(s) so far`, once on the first drop and then every 500th

This is by design so that one slow consumer cannot stall the MIDI stream, so speed up the consumer, reduce the message rate, or accept the loss

Note that the frames carry no sequence numbers, so a consumer cannot tell from the data that frames were lost

**Output looks like garbage, or the program reads huge lengths**

The reader is starting mid-frame, which usually means partial reads were treated as complete frames; use the read-exactly-N-bytes loop from the matching language example

**The ninth consumer cannot connect**

At most 8 concurrent consumers are supported; close one that is not in use
