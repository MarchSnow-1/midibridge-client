// 本文件覆盖 IPC 连接生命周期，包括槽位释放、幽灵连接丢帧、快速路径恢复、协程回收与并发抖动
// 这些用例针对 ipc.go 中连接注销与对端探测的实现，用于确认断开的消费者不会残留为活跃条目
//
// This file covers the IPC connection lifecycle: slot release, phantom-connection drops, fast-path recovery, goroutine reclamation and concurrent churn
// The cases target the deregistration and peer-detection logic in ipc.go, confirming that a disconnected consumer does not remain an active entry
package main

import (
	"fmt"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestIPCConnSlotsReleasedAfterDisconnect 覆盖连接槽位泄漏
// 消费者断开后须从 s.conns 注销，未注销时累计 8 次连接后 addConn 会拒绝新消费者
//
// TestIPCConnSlotsReleasedAfterDisconnect covers connection slot leakage
// A disconnected consumer must be removed from s.conns; without removal addConn refuses new consumers once 8 connections have accumulated
//
// 未修复时该用例在第 9 个消费者处失败
// Without the fix this case fails at the ninth consumer
func TestIPCConnSlotsReleasedAfterDisconnect(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	// 依次连入远多于上限（8）的消费者，每次读完 hello 就断开
	for i := 0; i < ipcMaxConnections*3; i++ {
		conn, err := net.Dial("tcp", s.Addr())
		if err != nil {
			t.Fatalf("round %d: dial: %v", i, err)
		}
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		kind, _, err := readFrame(conn)
		if err != nil {
			conn.Close()
			t.Fatalf("round %d: read hello: %v (slot leaked? no deregistration)", i, err)
		}
		if kind != ipcKindControlJSON {
			conn.Close()
			t.Fatalf("round %d: first frame kind = %#x, want control", i, kind)
		}
		conn.Close()

		// 等待注销完成（写协程退出是异步的）
		waitFor(t, 2*time.Second, func() bool {
			s.mu.RLock()
			n := len(s.conns)
			s.mu.RUnlock()
			return n == 0
		})
	}

	s.mu.RLock()
	n := len(s.conns)
	s.mu.RUnlock()
	if n != 0 {
		t.Fatalf("after all consumers disconnected, s.conns has %d stale entries", n)
	}
}

// TestIPCNoSpuriousDropsAfterConsumerLeaves 确认消费者离开后不再计入丢帧
// 未修复时已断开的连接被当作活跃消费者，dropped 会持续增长并记录误导性告警
//
// TestIPCNoSpuriousDropsAfterConsumerLeaves confirms a departed consumer no longer counts as a drop
// Without the fix a disconnected entry counted as an active consumer, so dropped kept growing and logged misleading warnings

func TestIPCNoSpuriousDropsAfterConsumerLeaves(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, _, err := readFrame(conn); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	conn.Close()

	waitFor(t, 2*time.Second, func() bool {
		s.mu.RLock()
		n := len(s.conns)
		s.mu.RUnlock()
		return n == 0
	})

	before := s.Dropped()
	for i := 0; i < 3000; i++ {
		s.Publish([]byte{0x90, 60, 100})
	}
	if got := s.Dropped() - before; got != 0 {
		t.Fatalf("departed consumer caused %d spurious drop(s)", got)
	}
}

// TestIPCFastPathRestoredAfterConsumerLeaves 确认无消费者时回到零分配快速路径
// 无消费者时 Publish 在加锁判空后直接返回，不构造帧切片
//
// TestIPCFastPathRestoredAfterConsumerLeaves confirms the zero-allocation fast path returns with no consumers
// With no consumers Publish returns right after the locked emptiness check and builds no frame slice
func TestIPCFastPathRestoredAfterConsumerLeaves(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, _, err := readFrame(conn); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	conn.Close()

	waitFor(t, 2*time.Second, func() bool {
		s.mu.RLock()
		n := len(s.conns)
		s.mu.RUnlock()
		return n == 0
	})

	msg := []byte{0x90, 60, 100}
	allocs := testing.AllocsPerRun(1000, func() {
		s.Publish(msg)
	})
	if allocs > 0 {
		t.Fatalf("no-consumer Publish allocates %.2f per call, want 0", allocs)
	}
}

// TestIPCNoGoroutineLeakOnIdleDisconnect 确认空闲时对端断开也能回收读协程与写协程
// 队列为空时依靠读协程观察到 FIN 并触发关闭，两个协程随之退出
//
// TestIPCNoGoroutineLeakOnIdleDisconnect confirms the read and write goroutines are reclaimed when an idle peer disconnects
// With an empty queue the read goroutine observes the FIN and triggers the close, so both goroutines exit

func TestIPCNoGoroutineLeakOnIdleDisconnect(t *testing.T) {
	// 给运行时一点时间稳定基线
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	base := runtime.NumGoroutine()

	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	for i := 0; i < 3; i++ {
		conn, err := net.Dial("tcp", s.Addr())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		if _, _, err := readFrame(conn); err != nil {
			conn.Close()
			t.Fatalf("read hello: %v", err)
		}
		// 不发布任何数据即断开——这正是原缺陷触发的场景
		conn.Close()
	}

	// 等待所有连接注销且协程退出
	waitFor(t, 5*time.Second, func() bool {
		s.mu.RLock()
		n := len(s.conns)
		s.mu.RUnlock()
		return n == 0 && runtime.NumGoroutine() <= base+1
	})

	if got := runtime.NumGoroutine(); got > base+1 {
		t.Fatalf("goroutines = %d after disconnects, base was %d (leak)", got, base)
	}
}

// TestIPCSimultaneousLimitStillEnforced 确认并发上限仍按连接数生效
// 第 9 个连接由 addConn 拒绝并关闭，读取时得到 EOF 而不是握手帧
//
// TestIPCSimultaneousLimitStillEnforced confirms the concurrent limit still applies by connection count
// addConn refuses and closes the ninth connection, so a read gets EOF instead of the handshake frame
func TestIPCSimultaneousLimitStillEnforced(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	conns := make([]net.Conn, 0, ipcMaxConnections)
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	for i := 0; i < ipcMaxConnections; i++ {
		conn, err := net.Dial("tcp", s.Addr())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		if _, _, err := readFrame(conn); err != nil {
			conn.Close()
			t.Fatalf("consumer %d: read hello: %v", i, err)
		}
		conns = append(conns, conn)
	}

	// 第 9 个连接应被拒绝：服务端关闭它，读 hello 会失败
	over, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial over limit: %v", err)
	}
	defer over.Close()
	over.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := readFrame(over); err == nil {
		t.Fatal("connection beyond ipcMaxConnections was accepted")
	}
}

// TestIPCConcurrentChurnNoPanic 在并发发布与连接抖动下确认无 panic 与死锁
// 多个发布协程与连接抖动并发运行，结束后所有连接应被注销
//
// TestIPCConcurrentChurnNoPanic confirms there is no panic and no deadlock under concurrent publishing and connection churn
// Several publishers run alongside connection churn, and every connection is deregistered once the run ends
func TestIPCConcurrentChurnNoPanic(t *testing.T) {
	s, err := NewIPCServer("127.0.0.1:0", "test")
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer s.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 发布者
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := []byte{0x90, 60, 100}
			for {
				select {
				case <-stop:
					return
				default:
					s.Publish(msg)
				}
			}
		}()
	}

	// 连接抖动
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			conn, err := net.Dial("tcp", s.Addr())
			if err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			readFrame(conn)
			conn.Close()
		}
	}()

	time.Sleep(600 * time.Millisecond)
	close(stop)
	wg.Wait()

	// 抖动结束后所有连接都应被注销
	waitFor(t, 5*time.Second, func() bool {
		s.mu.RLock()
		n := len(s.conns)
		s.mu.RUnlock()
		return n == 0
	})
}

// waitFor 轮询等待 cond 在超时前成立
//
// waitFor polls until cond holds or the timeout elapses
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

var _ = fmt.Sprint
