package net

import (
	"bufio"
	"net"
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

	handleOnce.Do(func() {
		countChan = make(chan *hConn, FreeConnectCount+5)
		handle(countChan, nil)
	})

	r := readPool.Get().(*bufio.Reader)
	r.Reset(c)
	hc := hConn{Conn: c, mu: &sync.Mutex{}}
	conn := &BufferedConn{r: r, hConn: hc}
	go func() {
		countChan <- &conn.hConn
	}()
	return conn
}

var (
	handleOnce       sync.Once
	MaxConnectCount  = 70
	FreeConnectCount = 30
	countChan        chan *hConn
)

func handle(hchan chan *hConn, once *sync.Once) {

	h := func() {
		go func() {
			conns := make([]*hConn, 0)
			lastClear := time.Now().Unix()

			for {

				select {
				case c := <-hchan:
					conns = append(conns, c)
				case <-time.After(time.Millisecond * 500):
				}

				current := time.Now().Unix()
				if len(conns) > MaxConnectCount {

					go func(conn *hConn) {
						time.Sleep(time.Second * 1)
						conn.Close()

					}(conns[0])
					conns = conns[1:]
				}
				if len(conns) > FreeConnectCount && current-lastClear > 2 {
					newCon := make([]*hConn, 0)
					for _, c := range conns {
						var canAdd = false
						c.mu.Lock()
						if current-c.aliveTime < 1 && !c.isClose {
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
				// go debug.FreeOSMemory()
			}
		}()
	}

	if once == nil {
		h()
	} else {
		once.Do(h)
	}
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
