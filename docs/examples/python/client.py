#!/usr/bin/env python3
"""MIDIBridge client IPC 的 Python 消费端示例

连接 client 的本地 TCP 流，读取握手帧，随后持续打印收到的每条 MIDI 消息，仅使用标准库
运行方式 python client.py [addr]

Python consumer example for the MIDIBridge client IPC

It connects to the client's local TCP stream, reads the handshake frame, then prints every MIDI message it receives, using the standard library only
Run with python client.py [addr]
"""

import socket
import struct
import sys

# 默认监听地址，client 使用其他端口时修改此处
#
# Default listen address, change it when the client uses another port
DEFAULT_ADDR = ("127.0.0.1", 9003)

# 控制帧承载 JSON，MIDI 帧承载一条原始 MIDI 消息
#
# Control frames carry JSON, MIDI frames carry one raw MIDI message
FRAME_KIND_CONTROL_JSON = 0x01
FRAME_KIND_MIDI = 0x02

# 分配内存前须校验长度字段，上限与服务端一致，否则一个畸形帧头就能让消费者按对端声称的长度分配
#
# A length field must be validated before allocating, and the cap matches the server, otherwise one malformed header makes the consumer allocate whatever the peer claims
MAX_FRAME_SIZE = 1 << 20


def recv_exact(sock, n):
    """精确读取 n 个字节

    socket.recv 只返回当前可用的数据，可能少于请求的字节数，因此必须循环直到读满
    把部分读取当成完整帧是最典型的帧处理错误

    Read exactly n bytes

    socket.recv returns only what is currently available, which may be fewer bytes than requested, so the loop runs until the buffer is full
    Treating a short read as a complete frame is the classic framing bug
    """
    buf = bytearray()
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            # 对端关闭了连接
            #
            # The peer closed the connection
            raise ConnectionError("connection closed")
        buf.extend(chunk)
    return bytes(buf)


def read_frame(sock):
    """读取完整的一帧

    布局为 [uint32 小端长度][uint8 类型][body...]

    Read one complete frame

    The layout is [uint32 little-endian length][uint8 kind][body...]
    """
    header = recv_exact(sock, 4)
    # 格式串中小于号表示小端序，I 表示无符号 32 位整数
    #
    # In the format string, a less-than sign means little endian and I means an unsigned 32-bit integer
    (length,) = struct.unpack("<I", header)
    if length == 0 or length > MAX_FRAME_SIZE:
        raise ValueError(f"invalid frame length {length}")

    payload = recv_exact(sock, length)
    return payload[0], payload[1:]


def main():
    addr = DEFAULT_ADDR
    if len(sys.argv) > 1:
        host, _, port = sys.argv[1].rpartition(":")
        addr = (host, int(port))

    with socket.create_connection(addr) as sock:
        print(f"connected to {addr[0]}:{addr[1]}")
        while True:
            try:
                kind, body = read_frame(sock)
            except (ConnectionError, ValueError) as exc:
                # client 关闭了连接，或数据流已损坏
                #
                # The client closed the connection, or the stream is corrupted
                print(f"read: {exc}", file=sys.stderr)
                return

            # 按帧类型分发，未知类型跳过而非视为致命错误，这样协议新增帧类型时旧消费者仍可工作
            #
            # Dispatch on the kind and skip unknown types rather than treating them as fatal, so an older consumer keeps working when the protocol adds a frame type
            if kind == FRAME_KIND_CONTROL_JSON:
                print(f"control: {body.decode('utf-8', 'replace')}")
            elif kind == FRAME_KIND_MIDI:
                # 以空格分隔的大写十六进制打印，例如 90 3C 64
                #
                # Print as space-separated uppercase hex, for example 90 3C 64
                print(f"midi   : {body.hex(' ').upper()}")
            else:
                print(f"unknown frame kind 0x{kind:02X}, {len(body)} bytes")


if __name__ == "__main__":
    main()
