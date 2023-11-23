package connmanager

import (
	"net"
	"sync"
	"time"
)

type HConn struct {
	AliveTime int64
	net.Conn
	IsClose bool
	Mu      *sync.Mutex
}

func (c HConn) Close() error {
	return c.Conn.Close()
}

func Handle(hchan chan *HConn, once *sync.Once, MaxConnectCount, FreeConnectCount int) {

	h := func() {
		go func() {
			conns := make([]*HConn, 0)
			lastClear := time.Now().Unix()

			for {

				select {
				case c := <-hchan:
					conns = append(conns, c)
				case <-time.After(time.Millisecond * 500):
				}

				current := time.Now().Unix()
				if len(conns) > MaxConnectCount {

					go func(conn *HConn) {
						time.Sleep(time.Second * 1)
						conn.Close()

					}(conns[0])
					conns = conns[1:]
				}
				if len(conns) > FreeConnectCount && current-lastClear > 2 {
					newCon := make([]*HConn, 0)
					for _, c := range conns {
						var canAdd = false
						c.Mu.Lock()
						if current-c.AliveTime < 1 && !c.IsClose {
							canAdd = true
						}
						c.Mu.Unlock()
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
