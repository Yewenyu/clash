package net

import (
	"bufio"
	"net"
	"sync"
	"time"

	"github.com/Dreamacro/clash/common/buf"
	connmanager "github.com/Dreamacro/clash/common/connManager"
)

var readPool = sync.Pool{
	New: func() any {
		size := TCPBufferSize
		if size == 0 {
			size = 4096
		}
		return bufio.NewReaderSize(nil, size)
	},
}

func (c *BufferedConn) Close() error {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	if c.IsClose {
		return nil
	}
	// c.r.Reset(nil)
	readPool.Put(c.r)
	c.IsClose = true
	return c.ExtendedConn.Close()
}

var _ ExtendedConn = (*BufferedConn)(nil)

type BufferedConn struct {
	r *bufio.Reader
	ExtendedConn
	peeked bool
	connmanager.HConn
}

func NewBufferedConn(c net.Conn) *BufferedConn {
	if bc, ok := c.(*BufferedConn); ok {
		return bc
	}
	r := readPool.Get().(*bufio.Reader)
	r.Reset(c)
	hc := connmanager.HConn{ConnManagerInterface: c, Mu: &sync.Mutex{}}
	conn := &BufferedConn{r, NewExtendedConn(c), false, hc}

	return conn
}

func WarpConnWithBioReader(c net.Conn, br *bufio.Reader) net.Conn {
	if br != nil && br.Buffered() > 0 {
		if bc, ok := c.(*BufferedConn); ok && bc.r == br {
			return bc
		}
		hc := connmanager.HConn{ConnManagerInterface: c, Mu: &sync.Mutex{}}
		conn := &BufferedConn{br, NewExtendedConn(c), false, hc}
		return conn
	}
	return c
}

// Reader returns the internal bufio.Reader.
func (c *BufferedConn) Reader() *bufio.Reader {
	return c.r
}

func (c *BufferedConn) ResetPeeked() {
	c.peeked = false
}

func (c *BufferedConn) Peeked() bool {
	return c.peeked
}

// Peek returns the next n bytes without advancing the reader.
func (c *BufferedConn) Peek(n int) ([]byte, error) {
	c.peeked = true
	return c.r.Peek(n)
}

func (c *BufferedConn) Discard(n int) (discarded int, err error) {
	return c.r.Discard(n)
}

func (c *BufferedConn) Read(p []byte) (int, error) {
	c.Mu.Lock()
	c.AliveTime = time.Now().Unix()
	c.Mu.Unlock()
	return c.r.Read(p)
}

func (c *BufferedConn) ReadByte() (byte, error) {
	c.Mu.Lock()
	c.AliveTime = time.Now().Unix()
	c.Mu.Unlock()
	return c.r.ReadByte()
}

func (c *BufferedConn) UnreadByte() error {
	return c.r.UnreadByte()
}

func (c *BufferedConn) Buffered() int {
	return c.r.Buffered()
}

func (c *BufferedConn) ReadBuffer(buffer *buf.Buffer) (err error) {
	if c.r != nil && c.r.Buffered() > 0 {
		_, err = buffer.ReadOnceFrom(c.r)
		return
	}
	return c.ExtendedConn.ReadBuffer(buffer)
}

func (c *BufferedConn) ReadCached() *buf.Buffer { // call in sing/common/bufio.Copy
	if c.r != nil && c.r.Buffered() > 0 {
		length := c.r.Buffered()
		b, _ := c.r.Peek(length)
		_, _ = c.r.Discard(length)
		return buf.As(b)
	}
	c.r = nil // drop bufio.Reader to let gc can clean up its internal buf
	return nil
}

func (c *BufferedConn) Upstream() any {
	return c.ExtendedConn
}

func (c *BufferedConn) ReaderReplaceable() bool {
	if c.r != nil && c.r.Buffered() > 0 {
		return false
	}
	return true
}

func (c *BufferedConn) WriterReplaceable() bool {
	return true
}
