// MIDIBridge client IPC 的 TypeScript 消费端示例
// 连接 client 的本地 TCP 流，读取握手帧，随后持续打印收到的每条 MIDI 消息
// 仅使用 Node.js 内置模块，类型定义在本文件内，无需 @types/node
// 运行方式 npx tsc client.ts --outDir dist && node dist/client.js [host] [port]
//
// TypeScript consumer example for the MIDIBridge client IPC
// It connects to the client's local TCP stream, reads the handshake frame, then prints every MIDI message it receives
// Only Node.js built-in modules are used and the types are declared in this file, so no @types/node is needed
// Run with npx tsc client.ts --outDir dist && node dist/client.js [host] [port]

// 声明用到的最小 Node.js API 子集，避免依赖 @types/node
//
// Declare the minimal Node.js API subset that is used, so @types/node is not required
interface BufferLike {
  length: number;
  readUInt32LE(offset: number): number;
  subarray(start?: number, end?: number): BufferLike;
  toString(encoding?: string): string;
  [index: number]: number;
}

interface BufferConstructor {
  alloc(size: number): BufferLike;
  concat(list: BufferLike[]): BufferLike;
  from(input: string): BufferLike;
}

interface SocketLike {
  on(event: 'data', listener: (chunk: BufferLike) => void): void;
  on(event: 'error', listener: (err: { message: string }) => void): void;
  on(event: 'close', listener: () => void): void;
  destroy(): void;
}

interface NetLike {
  connect(options: { host: string; port: number }, onConnected: () => void): SocketLike;
}

declare const Buffer: BufferConstructor;
declare const process: {
  argv: string[];
  exit(code: number): never;
};
declare function require(name: string): unknown;

const net = require('net') as NetLike;

// 默认监听地址，client 使用其他端口时通过命令行参数覆盖
//
// Default listen address, overridden via command-line arguments when the client uses another port
const DEFAULT_HOST = '127.0.0.1';
const DEFAULT_PORT = 9003;

// 控制帧承载 JSON，MIDI 帧承载一条原始 MIDI 消息
//
// Control frames carry JSON, MIDI frames carry one raw MIDI message
const FRAME_KIND_CONTROL_JSON = 0x01;
const FRAME_KIND_MIDI = 0x02;

// 分配内存前须校验长度字段，上限与服务端一致
// 否则一个畸形帧头就能让消费者按对端声称的长度分配而造成内存耗尽
//
// A length field must be validated before allocating, and the cap matches the server
// Otherwise one malformed header makes the consumer allocate whatever the peer claims
const MAX_FRAME_SIZE = 1 << 20;

const host = process.argv[2] ?? DEFAULT_HOST;
const port = process.argv[3] ? Number(process.argv[3]) : DEFAULT_PORT;

// data 事件交付的是一次 TCP 分段里到达的任意数据，一个 chunk 可能包含半帧、三帧或帧边界落在中间
// 因此必须把字节累积到缓冲区，只在凑满一整帧时才切出来解析，直接解析每个 chunk 是最典型的帧处理错误
//
// A data event delivers whatever arrived in one TCP segment, so a chunk may hold half a frame, three frames, or a frame boundary in the middle
// Bytes are therefore appended to a growing buffer and sliced only once a whole frame is present; parsing each chunk directly is the classic framing bug
let pending: BufferLike = Buffer.alloc(0);

const socket = net.connect({ host, port }, () => {
  console.log(`connected to ${host}:${port}`);
});

socket.on('data', (chunk: BufferLike) => {
  pending = Buffer.concat([pending, chunk]);

  for (;;) {
    // 帧头尚未凑齐
    //
    // The header is not complete yet
    if (pending.length < 4) return;

    // 长度前缀为小端序，统计的是 kind 与 body 的字节数
    //
    // The length prefix is little-endian and counts the kind byte plus the body
    const length = pending.readUInt32LE(0);
    if (length === 0 || length > MAX_FRAME_SIZE) {
      console.error(`invalid frame length ${length}`);
      socket.destroy();
      return;
    }

    // 帧体尚未凑齐
    //
    // The body is not complete yet
    if (pending.length < 4 + length) return;

    const payload = pending.subarray(4, 4 + length);
    pending = pending.subarray(4 + length);

    const kind = payload[0];
    const body = payload.subarray(1);

    // 按帧类型分发，未知类型跳过而非视为致命错误，这样协议新增帧类型时旧消费者仍可工作
    //
    // Dispatch on the kind and skip unknown types rather than treating them as fatal, so an older consumer keeps working when the protocol adds a frame type
    if (kind === FRAME_KIND_CONTROL_JSON) {
      console.log(`control: ${body.toString('utf8')}`);
    } else if (kind === FRAME_KIND_MIDI) {
      // 以空格分隔的大写十六进制打印，例如 90 3C 64
      //
      // Print as space-separated uppercase hex, for example 90 3C 64
      const hex: string[] = [];
      for (let i = 0; i < body.length; i++) {
        hex.push(body[i].toString(16).padStart(2, '0').toUpperCase());
      }
      console.log(`midi   : ${hex.join(' ')}`);
    } else {
      console.log(`unknown frame kind 0x${kind.toString(16).toUpperCase()}, ${body.length} bytes`);
    }
  }
});

socket.on('error', (err) => {
  // 连接被拒绝、连接重置，或 client 正在关闭
  //
  // The connection was refused or reset, or the client is shutting down
  console.error(`socket: ${err.message}`);
});

socket.on('close', () => {
  console.log('connection closed');
});
