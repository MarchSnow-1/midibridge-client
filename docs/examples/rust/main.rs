// MIDIBridge client IPC 的 Rust 消费端示例
// 连接 client 的本地 TCP 流，读取握手帧，随后持续打印收到的每条 MIDI 消息，仅使用标准库，不需要任何 crate
// 构建方式 rustc -O main.rs -o consumer，无需 Cargo.toml
// 运行方式 ./consumer [host] [port]
//
// Rust consumer example for the MIDIBridge client IPC
// It connects to the client's local TCP stream, reads the handshake frame, then prints every MIDI message it receives, with the standard library only and no crates
// Build with rustc -O main.rs -o consumer, no Cargo.toml needed
// Run with ./consumer [host] [port]

use std::env;
use std::io::{self, Read};
use std::net::TcpStream;

// 默认监听地址，client 使用其他端口时通过命令行参数覆盖
//
// Default listen address, overridden by command-line arguments when the client uses another port
const DEFAULT_HOST: &str = "127.0.0.1";
const DEFAULT_PORT: u16 = 9003;

// 控制帧承载 JSON，MIDI 帧承载一条原始 MIDI 消息
//
// Control frames carry JSON, MIDI frames carry one raw MIDI message
const FRAME_KIND_CONTROL_JSON: u8 = 0x01;
const FRAME_KIND_MIDI: u8 = 0x02;

// 分配内存前须校验长度字段，上限与服务端一致
//
// A length field must be validated before allocating, and the cap matches the server
const MAX_FRAME_SIZE: u32 = 1 << 20;

fn main() -> io::Result<()> {
    let args: Vec<String> = env::args().collect();
    let host = args.get(1).map(String::as_str).unwrap_or(DEFAULT_HOST);
    let port: u16 = args
        .get(2)
        .and_then(|s| s.parse().ok())
        .unwrap_or(DEFAULT_PORT);

    let mut stream = TcpStream::connect((host, port))?;
    // 帧很小且延迟敏感，因此关闭 Nagle 算法
    //
    // Frames are small and latency matters, so Nagle is turned off
    stream.set_nodelay(true)?;
    println!("connected to {}:{}", host, port);

    let mut header = [0u8; 4];
    loop {
        // read_exact 会把缓冲区完全填满，而 read 可能返回少于请求的字节数
        // 把短读当成完整帧是最典型的帧处理错误，read_exact 正是为此存在
        //
        // read_exact fills the buffer completely, while read may return fewer bytes than requested
        // Treating a short read as a whole frame is the classic framing bug, and read_exact exists precisely to avoid it
        if let Err(e) = stream.read_exact(&mut header) {
            // client 关闭了连接
            //
            // The client closed the connection
            eprintln!("read: {}", describe_io_error(&e));
            return Ok(());
        }

        // 长度前缀为小端序，统计的是 kind 与 body 的字节数
        //
        // The length prefix is little-endian and counts the kind byte plus the body
        let length = u32::from_le_bytes(header);
        if length == 0 || length > MAX_FRAME_SIZE {
            eprintln!("read: invalid frame length {}", length);
            return Ok(());
        }

        let mut payload = vec![0u8; length as usize];
        if let Err(e) = stream.read_exact(&mut payload) {
            eprintln!("read: {}", describe_io_error(&e));
            return Ok(());
        }

        let kind = payload[0];
        let body = &payload[1..];

        // 按帧类型分发，未知类型跳过而非视为致命错误，这样协议新增帧类型时旧消费者仍可工作
        //
        // Dispatch on the kind and skip unknown types rather than treating them as fatal, so an older consumer keeps working when the protocol adds a frame type
        match kind {
            FRAME_KIND_CONTROL_JSON => match std::str::from_utf8(body) {
                Ok(text) => println!("control: {}", text),
                Err(_) => println!("control: <invalid utf-8>"),
            },
            FRAME_KIND_MIDI => {
                // 以空格分隔的大写十六进制打印，例如 90 3C 64
                //
                // Print as space-separated uppercase hex, for example 90 3C 64
                println!("midi   : {}", hex_dump(body))
            }
            other => println!(
                "unknown frame kind 0x{:02X}, {} bytes",
                other,
                body.len()
            ),
        }
    }
}

/// Rust 把正常关闭报告为 UnexpectedEof，这里与其他语言示例保持一致地打印，便于跨语言比对输出
///
/// Rust reports a clean shutdown as UnexpectedEof, and it is printed the way the other examples do so the output stays comparable across languages
fn describe_io_error(e: &io::Error) -> String {
    if e.kind() == io::ErrorKind::UnexpectedEof {
        "connection closed".to_string()
    } else {
        e.to_string()
    }
}

/// 格式化为空格分隔的大写十六进制字节对
///
/// Format as uppercase hex pairs separated by spaces
fn hex_dump(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 3);
    for (i, b) in bytes.iter().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        out.push_str(&format!("{:02X}", b));
    }
    out
}
