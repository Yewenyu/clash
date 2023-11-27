package net

import (
	"bufio"
	"net"
	"sync"
	"time"

	connmanager "github.com/Dreamacro/clash/common/connManager"
)

type BufferedConn struct {
	r *bufio.Reader
	net.Conn
	connmanager.HConn
}

var readPool = sync.Pool{
	New: func() any {
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

	handleOnce.Do(func() {
		countChan = make(chan *connmanager.HConn, FreeConnectCount+5)
		connmanager.Handle(countChan, nil, MaxConnectCount, FreeConnectCount, 1)
	})

	r := readPool.Get().(*bufio.Reader)
	r.Reset(c)
	hc := connmanager.HConn{ConnManagerInterface: c, Mu: &sync.Mutex{}}
	conn := &BufferedConn{r: r, Conn: c, HConn: hc}
	go func() {
		countChan <- &conn.HConn
	}()
	return conn
}

var (
	handleOnce       sync.Once
	MaxConnectCount  = 70
	FreeConnectCount = 30
	countChan        chan *connmanager.HConn
)

func (c *BufferedConn) Close() error {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	if c.IsClose {
		return nil
	}
	// c.r.Reset(nil)
	readPool.Put(c.r)
	c.IsClose = true
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
