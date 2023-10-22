package net

import (
	"bufio"
	"net"
	"runtime/debug"
	"sync"
	"time"
)

type BufferedConn struct {
	r *bufio.Reader

	hConn
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
	handle(countChan, &handleOnce)

	r := readPool.Get().(*bufio.Reader)
	r.Reset(c)
	hc := hConn{Conn: c, mu: &sync.Mutex{}}
	conn := &BufferedConn{r: r, hConn: hc}
	countChan <- &conn.hConn
	return conn
}

var (
	handleOnce sync.Once

	countChan = make(chan *hConn, 10)
	maxCount  = 70
	freeCount = 30
)

func handle(hchan chan *hConn, once *sync.Once) {

	once.Do(func() {

		go func() {
			conns := make([]*hConn, 0)
			lastClear := time.Now().Unix()
			for {
				c := <-hchan
				conns = append(conns, c)
				current := time.Now().Unix()
				if len(conns) > maxCount {
					for i := 0; i < freeCount; i++ {
						conns[i].Close()
					}
					conns = conns[freeCount:]
					lastClear = current
					debug.FreeOSMemory()
				} else if len(conns) > freeCount && current-lastClear > 10 {
					newCon := make([]*hConn, 0)
					for _, c := range conns {
						var canAdd = false
						c.mu.Lock()
						if current-c.aliveTime < 2 && !c.isClose {
							canAdd = true
						}
						c.mu.Unlock()
						if canAdd {
							newCon = append(newCon, c)
						} else {
							c.Close()
						}

					}
					lastClear = current
					conns = newCon
				}
			}
		}()
	})
}

func (c *BufferedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isClose {
		return nil
	}
	// c.r.Reset(nil)
	readPool.Put(c.r)
	c.isClose = true
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
	c.mu.Lock()
	c.aliveTime = time.Now().Unix()
	c.mu.Unlock()
	return c.r.Read(p)
}

func (c *BufferedConn) ReadByte() (byte, error) {
	c.mu.Lock()
	c.aliveTime = time.Now().Unix()
	c.mu.Unlock()
	return c.r.ReadByte()
}

func (c *BufferedConn) UnreadByte() error {
	return c.r.UnreadByte()
}

func (c *BufferedConn) Buffered() int {
	return c.r.Buffered()
}
