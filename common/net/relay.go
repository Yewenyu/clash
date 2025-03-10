package net

import (
	"io"
	"net"
	"sync"
	"time"
)

var (
	TcpTimeout  = 5
	UdpTimeOut  = 5
	HttpTimeout = 5
	DNSTimeout  = 5
)

// Relay copies between left and right bidirectionally.
func Relay1(leftConn, rightConn net.Conn) {
	ch := make(chan error)

	go func() {
		// Wrapping to avoid using *net.TCPConn.(ReadFrom)
		// See also https://github.com/Dreamacro/clash/pull/1209
		_, err := io.Copy(WriteOnlyWriter{Writer: leftConn}, ReadOnlyReader{Reader: rightConn})
		leftConn.SetReadDeadline(time.Now())
		ch <- err
	}()

	io.Copy(WriteOnlyWriter{Writer: rightConn}, ReadOnlyReader{Reader: leftConn})
	rightConn.SetReadDeadline(time.Now())
	<-ch
}

var TCPBufferSize = 0
var pool = sync.Pool{
	New: func() any {
		return make([]byte, TCPBufferSize)
	},
}

func Relay(leftConn, rightConn net.Conn, useHttpTimeout bool, useDNSTimeout bool) {
	if TCPBufferSize == 0 {
		Relay1(leftConn, rightConn)
		return
	}
	// handle(cChan, &handleO)
	// 如果两个连接在指定时间内有数据交互，则继续连接，否则关闭连接
	timeout := TcpTimeout
	if useHttpTimeout {
		timeout = HttpTimeout
	}
	if useDNSTimeout {
		timeout = DNSTimeout
	}
	timeLock := time.NewTimer(time.Duration(timeout) * time.Second)

	handle := func(w, r net.Conn) {
		b := pool.Get().([]byte)
		defer pool.Put(b)
		defer w.Close()
	loop:
		for {
			select {
			case <-timeLock.C:
				break loop
			default:
				// 更新连接时间
				timeLock.Reset(time.Duration(timeout) * time.Second)
				r.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
				n, err := r.Read(b)
				if err != nil {
					// 如果读取超时，继续循环
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					break loop
				}
				_, err = w.Write(b[:n])
				if err != nil {
					break loop
				}
			}

		}
	}
	go handle(rightConn, leftConn)
	handle(leftConn, rightConn)
	rightConn.SetReadDeadline(time.Now())
}
