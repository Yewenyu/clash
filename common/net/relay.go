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

var (
	handleO sync.Once

	cChan = make(chan *hConn, 10)
)

type hConn struct {
	aliveTime int64
	net.Conn
	isClose bool
	mu      *sync.Mutex
}

func (c hConn) Close() error {
	return c.Conn.Close()
}

func Relay(leftConn, rightConn net.Conn) {
	if TCPBufferSize == 0 {
		Relay1(leftConn, rightConn)
		return
	}
	// handle(cChan, &handleO)
	mu := sync.Mutex{}
	left := hConn{aliveTime: time.Now().Unix(), Conn: leftConn, mu: &mu}
	right := hConn{aliveTime: time.Now().Unix(), Conn: rightConn, mu: &mu}
	// cChan <- &left

	handle := func(w, r hConn) {
		b := pool.Get().([]byte)
		defer pool.Put(b)
		defer w.Close()
		for {
			n, err := r.Read(b)
			if err != nil {
				break
			}
			r.aliveTime = time.Now().Unix()
			_, err = w.Write(b[:n])
			if err != nil {
				break
			}
		}
	}
	go handle(right, left)
	handle(left, right)

	rightConn.SetReadDeadline(time.Now())
}
