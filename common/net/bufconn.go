package net

import (
	"bufio"
	"net"
	"sync"
)

type BufferedConn struct {
	r *bufio.Reader
	net.Conn
}

var readerPool = sync.Pool{
	New: func() interface{} {
		size := TCPBufferSize
		if size == 0 {
			size = 4096
		}
		return bufio.NewReaderSize(nil, size)
	},
}

func NewBufferedConn(c net.Conn) *BufferedConn {
	if bc, ok := c.(*BufferedConn); ok {
		return bc
	}
	r := readerPool.Get().(*bufio.Reader)
	r.Reset(c)
	return &BufferedConn{r, c}
}

var mu = sync.Mutex{}
var closeCount = 0

func (c *BufferedConn) Close() error {
	// mu.Lock()
	// if closeCount > 20 {
	// 	go debug.FreeOSMemory()
	// 	closeCount = 0
	// }
	// closeCount += 1
	// mu.Unlock()

	readerPool.Put(c.r)
	return c.Conn.Close()
}

// Reader returns the internal bufio.Reader.
func (c *BufferedConn) Reader() *bufio.Reader {
	return c.r
}

// Peek returns the next n bytes without advancing the reader.
func (c *BufferedConn) Peek(n int) ([]byte, error) {
	return c.r.Peek(n)
}

func (c *BufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func (c *BufferedConn) ReadByte() (byte, error) {
	return c.r.ReadByte()
}

func (c *BufferedConn) UnreadByte() error {
	return c.r.UnreadByte()
}

func (c *BufferedConn) Buffered() int {
	return c.r.Buffered()
}
