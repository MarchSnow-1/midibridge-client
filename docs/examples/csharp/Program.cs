// MIDIBridge client IPC 的 C# 消费端示例
// 连接 client 的本地 TCP 流，读取握手帧，随后持续打印收到的每条 MIDI 消息，仅使用 BCL 类型，无需任何 NuGet 包
// 运行方式 dotnet run，工程文件说明见本目录 README
//
// C# consumer example for the MIDIBridge client IPC
// It connects to the client's local TCP stream, reads the handshake frame, then prints every MIDI message it receives, using BCL types only so no NuGet packages are needed
// Run with dotnet run; see the README in this folder for the project file
using System;
using System.IO;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;

// 默认监听地址，client 使用其他端口时通过命令行参数覆盖
//
// Default listen address, overridden by command-line arguments when the client uses another port
const string DefaultHost = "127.0.0.1";
const int DefaultPort = 9003;

// 控制帧承载 JSON，MIDI 帧承载一条原始 MIDI 消息
//
// Control frames carry JSON, MIDI frames carry one raw MIDI message
const byte FrameKindControlJson = 0x01;
const byte FrameKindMidi = 0x02;

// 分配内存前须校验长度字段，上限与服务端一致
//
// A length field must be validated before allocating, and the cap matches the server
const int MaxFrameSize = 1 << 20;

string host = args.Length > 0 ? args[0] : DefaultHost;
int port = args.Length > 1 && int.TryParse(args[1], out var parsedPort) ? parsedPort : DefaultPort;

using var client = new TcpClient();
// 帧很小且延迟敏感，因此关闭 Nagle 算法
//
// Frames are small and latency matters, so Nagle is turned off
client.NoDelay = true;
client.Connect(host, port);
Console.WriteLine($"connected to {host}:{port}");

using var stream = client.GetStream();

var header = new byte[4];
while (true)
{
    try
    {
        ReadExactly(stream, header, 0, header.Length);

        // 长度前缀为小端序，统计的是 kind 与 body 的字节数
        //
        // The length prefix is little-endian and counts the kind byte plus the body
        uint length = BitConverter.ToUInt32(header, 0);
        if (length == 0 || length > MaxFrameSize)
            throw new InvalidDataException($"invalid frame length {length}");

        var payload = new byte[length];
        ReadExactly(stream, payload, 0, payload.Length);

        byte kind = payload[0];
        var body = payload.AsSpan(1);

        // 按帧类型分发，未知类型跳过而非视为致命错误，这样协议新增帧类型时旧消费者仍可工作
        //
        // Dispatch on the kind and skip unknown types rather than treating them as fatal, so an older consumer keeps working when the protocol adds a frame type
        switch (kind)
        {
            case FrameKindControlJson:
                using (var doc = JsonDocument.Parse(body.ToArray()))
                {
                    Console.WriteLine($"control: {Encoding.UTF8.GetString(body)}");
                }
                break;

            case FrameKindMidi:
                // 以空格分隔的大写十六进制打印，例如 90 3C 64
                //
                // Print as space-separated uppercase hex, for example 90 3C 64
                Console.WriteLine($"midi   : {FormatHex(body)}");
                break;

            default:
                Console.WriteLine($"unknown frame kind 0x{kind:X2}, {body.Length} bytes");
                break;
        }
    }
    catch (Exception ex) when (ex is EndOfStreamException or IOException or InvalidDataException)
    {
        // client 关闭了连接，或数据流已损坏
        //
        // The client closed the connection, or the stream is corrupted
        Console.Error.WriteLine($"read: {ex.Message}");
        return;
    }
}

// 把缓冲区完全填满
//
// NetworkStream.Read 只返回当前可用的数据，通常少于请求的字节数
// 把它的返回值当成一整帧就是最典型的帧处理错误
//
// Fill the buffer completely
//
// NetworkStream.Read returns only what is currently available, which is often fewer bytes than requested
// Treating its return value as a whole frame is the classic framing bug
static void ReadExactly(Stream s, byte[] buffer, int offset, int count)
{
    while (count > 0)
    {
        int n = s.Read(buffer, offset, count);
        if (n <= 0) throw new EndOfStreamException("connection closed");
        offset += n;
        count -= n;
    }
}

// 格式化为空格分隔的大写十六进制字节对
//
// Format as uppercase hex pairs separated by spaces
static string FormatHex(ReadOnlySpan<byte> bytes)
{
    var sb = new StringBuilder(bytes.Length * 3);
    for (int i = 0; i < bytes.Length; i++)
    {
        if (i > 0) sb.Append(' ');
        sb.Append(bytes[i].ToString("X2"));
    }
    return sb.ToString();
}
