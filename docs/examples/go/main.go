// MIDIBridge client IPC 的 Go 消费端示例
// 连接 client 的本地 TCP 流，读取握手帧，随后持续打印收到的每条 MIDI 消息，仅使用标准库
// 运行方式 go run main.go [addr]
//
// Go consumer example for the MIDIBridge client IPC
// It connects to the client's local TCP stream, reads the handshake frame, then prints every MIDI message it receives, using the standard library only
// Run with go run main.go [addr]
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
)

const (
	// 默认监听地址，client 使用其他端口时修改此处
	//
	// Default listen address, change it when the client uses another port
	defaultAddr = "127.0.0.1:9003"

	// 控制帧承载 JSON，MIDI 帧承载一条原始 MIDI 消息
	//
	// Control frames carry JSON, MIDI frames carry one raw MIDI message
	frameKindControlJSON byte = 0x01
	frameKindMIDI        byte = 0x02

	// 分配内存前须校验长度字段，上限与服务端一致，否则一个畸形帧头就能让消费者按对端声称的长度分配而造成内存耗尽
	//
	// A length field must be validated before allocating, and the cap matches the server, otherwise one malformed header makes the consumer allocate whatever the peer claims
	maxFrameSize = 1 << 20
)

// readFrame 读取完整的一帧
// io.ReadFull 保证读满长度字段承诺的字节数，即使内核分多次返回，用普通 Read 会漏读剩余部分
//
// readFrame reads exactly one frame
// io.ReadFull fills the bytes promised by the length field even when the kernel returns them in parts, while a plain Read would leave the remainder unread
func readFrame(r io.Reader) (kind byte, body []byte, err error) {
	// 先读 4 字节长度前缀，小端序与协议一致
	//
	// Read the 4-byte length prefix first; little endian matches the protocol
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	length := binary.LittleEndian.Uint32(hdr[:])

	// 长度为 0 或超过上限时拒绝，避免畸形数据触发过大的分配
	//
	// Refuse a length of zero or above the cap so malformed data cannot trigger a large allocation
	if length == 0 || length > maxFrameSize {
		return 0, nil, fmt.Errorf("invalid frame length %d", length)
	}

	// 再读满 length 字节，其中首字节是 kind，其余是 body
	//
	// Then read the full length bytes, whose first byte is the kind and the rest the body
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func main() {
	addr := defaultAddr
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("connected to %s\n", addr)

	for {
		kind, body, err := readFrame(conn)
		if err != nil {
			// 对端关闭连接或数据流损坏，Go 把正常关闭报告为 io.EOF，中断在帧中途时报告 io.ErrUnexpectedEOF
			// 这两种情况统一措辞为 connection closed，与其他语言示例的输出保持一致
			//
			// The peer closed the connection or the stream is corrupted; Go reports a clean close as io.EOF and a close mid-frame as io.ErrUnexpectedEOF
			// Both are phrased as connection closed so the output matches the other language examples
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				fmt.Fprintln(os.Stderr, "read: connection closed")
			} else {
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
			}
			return
		}

		// 按 kind 分发，未知类型跳过而非视为致命错误，这样协议新增帧类型时旧消费者仍可工作
		//
		// Dispatch on the kind and skip unknown types rather than treating them as fatal, so an older consumer keeps working when the protocol adds a frame type
		switch kind {
		case frameKindControlJSON:
			fmt.Printf("control: %s\n", body)
		case frameKindMIDI:
			fmt.Printf("midi   : % X\n", body)
		default:
			fmt.Fprintf(os.Stderr, "skip: unknown frame kind 0x%02X\n", kind)
		}
	}
}
