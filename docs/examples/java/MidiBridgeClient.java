// MIDIBridge client IPC 的 Java 消费端示例
// 连接 client 的本地 TCP 流，读取握手帧，随后持续打印收到的每条 MIDI 消息，仅需 JDK，无任何外部依赖
// 构建方式 javac MidiBridgeClient.java
// 运行方式 java MidiBridgeClient [host] [port]
//
// Java consumer example for the MIDIBridge client IPC
// It connects to the client's local TCP stream, reads the handshake frame, then prints every MIDI message it receives, with the JDK only and no external dependencies
// Build with javac MidiBridgeClient.java
// Run with java MidiBridgeClient [host] [port]

import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.net.Socket;
import java.nio.charset.StandardCharsets;

public class MidiBridgeClient {

    // 默认监听地址，client 使用其他端口时通过命令行参数覆盖
    //
    // Default listen address, overridden by command-line arguments when the client uses another port
    private static final String DEFAULT_HOST = "127.0.0.1";
    private static final int DEFAULT_PORT = 9003;

    // 控制帧承载 JSON，MIDI 帧承载一条原始 MIDI 消息
    //
    // Control frames carry JSON, MIDI frames carry one raw MIDI message
    private static final byte FRAME_KIND_CONTROL_JSON = 0x01;
    private static final byte FRAME_KIND_MIDI = 0x02;

    // 分配内存前须校验长度字段，上限与服务端一致
    //
    // A length field must be validated before allocating, and the cap matches the server
    private static final long MAX_FRAME_SIZE = 1L << 20;

    public static void main(String[] args) {
        String host = args.length > 0 ? args[0] : DEFAULT_HOST;
        int port = args.length > 1 ? Integer.parseInt(args[1]) : DEFAULT_PORT;

        try (Socket socket = new Socket(host, port)) {
            // 帧很小且延迟敏感，因此关闭 Nagle 算法
            //
            // Frames are small and latency matters, so Nagle is turned off
            socket.setTcpNoDelay(true);
            System.out.println("connected to " + host + ":" + port);

            InputStream in = socket.getInputStream();
            byte[] header = new byte[4];

            while (true) {
                readFully(in, header, 0, header.length);

                // 长度前缀为小端序，统计的是 kind 与 body 的字节数
                // DataInputStream.readInt 读取大端序，因此这里显式拼装而不是直接使用它
                //
                // The length prefix is little-endian and counts the kind byte plus the body
                // DataInputStream.readInt reads big-endian, so the value is assembled explicitly instead of using it
                long length = (header[0] & 0xFFL)
                        | ((header[1] & 0xFFL) << 8)
                        | ((header[2] & 0xFFL) << 16)
                        | ((header[3] & 0xFFL) << 24);

                if (length == 0 || length > MAX_FRAME_SIZE) {
                    System.err.println("read: invalid frame length " + length);
                    return;
                }

                byte[] payload = new byte[(int) length];
                readFully(in, payload, 0, payload.length);

                byte kind = payload[0];
                int bodyLength = payload.length - 1;

                // 按帧类型分发，未知类型跳过而非视为致命错误，这样协议新增帧类型时旧消费者仍可工作
                //
                // Dispatch on the kind and skip unknown types rather than treating them as fatal, so an older consumer keeps working when the protocol adds a frame type
                if (kind == FRAME_KIND_CONTROL_JSON) {
                    String body = new String(payload, 1, bodyLength, StandardCharsets.UTF_8);
                    System.out.println("control: " + body);
                } else if (kind == FRAME_KIND_MIDI) {
                    // 以空格分隔的大写十六进制打印，例如 90 3C 64
                    //
                    // Print as space-separated uppercase hex, for example 90 3C 64
                    System.out.println("midi   : " + hexDump(payload, 1, bodyLength));
                } else {
                    System.out.printf("unknown frame kind 0x%02X, %d bytes%n",
                            kind, bodyLength);
                }
            }
        } catch (EOFException e) {
            // client 关闭了连接
            //
            // The client closed the connection
            System.err.println("read: connection closed");
        } catch (IOException e) {
            // 连接被拒绝、连接重置，或数据流已损坏
            //
            // The connection was refused or reset, or the stream is corrupted
            System.err.println("read: " + e.getMessage());
        }
    }

    /**
     * 把缓冲区完全填满
     *
     * InputStream.read 可能返回少于请求的字节数，把部分读取当成完整帧是最典型的帧处理错误
     * 这里实现与 DataInputStream.readFully 相同的语义，只是无需额外包一层
     *
     * Fill the buffer completely
     *
     * InputStream.read may return fewer bytes than requested, and treating a partial read as a complete frame is the classic framing bug
     * This mirrors DataInputStream.readFully semantics without wrapping one
     */
    private static void readFully(InputStream in, byte[] buffer, int offset, int count)
            throws IOException {
        while (count > 0) {
            int n = in.read(buffer, offset, count);
            if (n < 0) {
                throw new EOFException("connection closed");
            }
            offset += n;
            count -= n;
        }
    }

    /**
     * 格式化为空格分隔的大写十六进制字节对
     *
     * Format as uppercase hex pairs separated by spaces
     */
    private static String hexDump(byte[] buffer, int offset, int length) {
        StringBuilder sb = new StringBuilder(length * 3);
        for (int i = 0; i < length; i++) {
            if (i > 0) {
                sb.append(' ');
            }
            sb.append(String.format("%02X", buffer[offset + i]));
        }
        return sb.toString();
    }
}
