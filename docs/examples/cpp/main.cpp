// MIDIBridge client IPC 的 C++ 消费端示例
// 连接 client 的本地 TCP 流，读取握手帧，随后持续打印收到的每条 MIDI 消息，仅使用标准库，Windows 上用 Winsock2，其他平台用 POSIX socket
// 构建方式 Windows 上为 g++ main.cpp -lws2_32 -o consumer.exe 或 cl /utf-8 main.cpp ws2_32.lib，POSIX 上为 g++ main.cpp -o consumer
// 运行方式 ./consumer [host] [port]
//
// C++ consumer example for the MIDIBridge client IPC
// It connects to the client's local TCP stream, reads the handshake frame, then prints every MIDI message it receives, using the standard library only, with Winsock2 on Windows and POSIX sockets elsewhere
// Build with g++ main.cpp -lws2_32 -o consumer.exe or cl /utf-8 main.cpp ws2_32.lib on Windows, and g++ main.cpp -o consumer on POSIX
// Run with ./consumer [host] [port]
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

#ifdef _WIN32
#include <winsock2.h>
#include <ws2tcpip.h>
using socket_t = SOCKET;
constexpr socket_t kInvalidSocket = INVALID_SOCKET;
#else
#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <unistd.h>
using socket_t = int;
constexpr socket_t kInvalidSocket = -1;
#endif

namespace {

// 默认监听地址，client 使用其他端口时通过命令行参数覆盖
//
// Default listen address, overridden by command-line arguments when the client uses another port
constexpr const char* kDefaultHost = "127.0.0.1";
constexpr int kDefaultPort = 9003;

// 控制帧承载 JSON，MIDI 帧承载一条原始 MIDI 消息
//
// Control frames carry JSON, MIDI frames carry one raw MIDI message
constexpr uint8_t kFrameKindControlJson = 0x01;
constexpr uint8_t kFrameKindMidi = 0x02;

// 分配内存前须校验长度字段，上限与服务端一致
//
// A length field must be validated before allocating, and the cap matches the server
constexpr uint32_t kMaxFrameSize = 1u << 20;

void closeSocket(socket_t s) {
#ifdef _WIN32
  closesocket(s);
#else
  close(s);
#endif
}

// recvAll 精确读取 n 个字节
// recv 可能返回少于请求的字节数，即部分读取，忽略这一点并假设缓冲区已填满就是最典型的帧处理错误，因此这里必须循环
//
// recvAll reads exactly n bytes
// recv may return fewer bytes than requested, which is a partial read, and assuming the buffer is full is the classic framing bug, so the loop is required
bool recvAll(socket_t sock, uint8_t* out, size_t n) {
  size_t got = 0;
  while (got < n) {
    int r = ::recv(sock, reinterpret_cast<char*>(out + got),
                   static_cast<int>(n - got), 0);
    // 返回 0 表示对端正常关闭，小于 0 表示错误
    //
    // A return of 0 means an orderly shutdown by the peer and a negative value means an error
    if (r <= 0) return false;
    got += static_cast<size_t>(r);
  }
  return true;
}

// 格式化为空格分隔的大写十六进制字节对
//
// Format as uppercase hex pairs separated by spaces
std::string hexDump(const std::vector<uint8_t>& bytes) {
  static const char* kHex = "0123456789ABCDEF";
  std::string out;
  out.reserve(bytes.size() * 3);
  for (size_t i = 0; i < bytes.size(); ++i) {
    if (i) out.push_back(' ');
    out.push_back(kHex[(bytes[i] >> 4) & 0x0F]);
    out.push_back(kHex[bytes[i] & 0x0F]);
  }
  return out;
}

}  // namespace

int main(int argc, char** argv) {
  const char* host = argc > 1 ? argv[1] : kDefaultHost;
  int port = argc > 2 ? std::atoi(argv[2]) : kDefaultPort;

#ifdef _WIN32
  // 任何 socket 调用之前必须先初始化 Winsock
  //
  // Winsock must be initialised before any socket call
  WSADATA wsaData;
  if (WSAStartup(MAKEWORD(2, 2), &wsaData) != 0) {
    std::fprintf(stderr, "WSAStartup failed\n");
    return 1;
  }
#endif

  addrinfo hints{};
  hints.ai_family = AF_UNSPEC;
  hints.ai_socktype = SOCK_STREAM;
  hints.ai_protocol = IPPROTO_TCP;

  addrinfo* result = nullptr;
  const std::string portStr = std::to_string(port);
  if (getaddrinfo(host, portStr.c_str(), &hints, &result) != 0 || !result) {
    std::fprintf(stderr, "getaddrinfo(%s) failed\n", host);
#ifdef _WIN32
    WSACleanup();
#endif
    return 1;
  }

  socket_t sock = kInvalidSocket;
  for (addrinfo* ai = result; ai; ai = ai->ai_next) {
    sock = ::socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
    if (sock == kInvalidSocket) continue;
    if (::connect(sock, ai->ai_addr, static_cast<int>(ai->ai_addrlen)) == 0) break;
    closeSocket(sock);
    sock = kInvalidSocket;
  }
  freeaddrinfo(result);

  if (sock == kInvalidSocket) {
    std::fprintf(stderr, "connect to %s:%d failed\n", host, port);
#ifdef _WIN32
    WSACleanup();
#endif
    return 1;
  }

  // 帧很小且延迟敏感，因此关闭 Nagle 算法
  //
  // Frames are small and latency matters, so Nagle is turned off
  int one = 1;
  ::setsockopt(sock, IPPROTO_TCP, TCP_NODELAY,
               reinterpret_cast<const char*>(&one), sizeof(one));

  std::printf("connected to %s:%d\n", host, port);

  std::vector<uint8_t> header(4);
  while (true) {
    if (!recvAll(sock, header.data(), header.size())) {
      // client 关闭了连接
      //
      // The client closed the connection
      std::fprintf(stderr, "read: connection closed\n");
      break;
    }

    // 长度前缀为小端序，统计的是 kind 与 body 的字节数
    // 这里逐字节拼装而不是 memcpy 到 uint32_t，以便在大端主机上同样正确
    //
    // The length prefix is little-endian and counts the kind byte plus the body
    // It is assembled byte by byte rather than memcpy into a uint32_t so the code stays correct on big-endian hosts
    uint32_t length = static_cast<uint32_t>(header[0]) |
                      (static_cast<uint32_t>(header[1]) << 8) |
                      (static_cast<uint32_t>(header[2]) << 16) |
                      (static_cast<uint32_t>(header[3]) << 24);

    if (length == 0 || length > kMaxFrameSize) {
      std::fprintf(stderr, "read: invalid frame length %u\n", length);
      break;
    }

    std::vector<uint8_t> payload(length);
    if (!recvAll(sock, payload.data(), payload.size())) {
      std::fprintf(stderr, "read: connection closed\n");
      break;
    }

    const uint8_t kind = payload[0];
    std::vector<uint8_t> body(payload.begin() + 1, payload.end());

    // 按帧类型分发，未知类型跳过而非视为致命错误，这样协议新增帧类型时旧消费者仍可工作
    //
    // Dispatch on the kind and skip unknown types rather than treating them as fatal, so an older consumer keeps working when the protocol adds a frame type
    if (kind == kFrameKindControlJson) {
      std::printf("control: %s\n",
                  std::string(body.begin(), body.end()).c_str());
    } else if (kind == kFrameKindMidi) {
      // 以空格分隔的大写十六进制打印，例如 90 3C 64
      //
      // Print as space-separated uppercase hex, for example 90 3C 64
      std::printf("midi   : %s\n", hexDump(body).c_str());
    } else {
      std::printf("unknown frame kind 0x%02X, %zu bytes\n", kind, body.size());
    }
  }

  closeSocket(sock);
#ifdef _WIN32
  WSACleanup();
#endif
  return 0;
}
