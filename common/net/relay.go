package net

import (
	"io"
	"net"
	"sync"
	"time"
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

func Relay(leftConn, rightConn net.Conn) {
	if TCPBufferSize == 0 {
		Relay1(leftConn, rightConn)
		return
	}
	ch := make(chan error)

	handle := func(w, r net.Conn) {
		b := pool.Get().([]byte)
		defer pool.Put(b)
		for {
			n, err := r.Read(b)
			if err != nil {
				break
			}
			_, err = w.Write(b[:n])
			if err != nil {
				break
			}
		}
	}
	go handle(rightConn, leftConn)
	handle(leftConn, rightConn)

	rightConn.SetReadDeadline(time.Now())
	<-ch
}
