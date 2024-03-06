package connmanager

import (
	"net"
	"sync"
	"time"
)

type ConnManagerInterface interface {
	Close() error
}

type HConn struct {
	AliveTime int64
	ConnManagerInterface
	IsClose bool
	Mu      *sync.Mutex
}

func Handle(hchan chan *HConn, once *sync.Once, MaxConnectCount, FreeConnectCount int, aliveTimeout int) {

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

						_, ok := conn.ConnManagerInterface.(net.PacketConn)
						if ok {
							print("")
						}
						conn.Close()

					}(conns[0])
					conns = conns[1:]
				}
				if len(conns) > FreeConnectCount && current-lastClear > 2 {
					newCon := make([]*HConn, 0)
					for _, c := range conns {
						var canAdd = false
						c.Mu.Lock()
						if current-c.AliveTime < int64(aliveTimeout) && !c.IsClose {
							canAdd = true
						}
						c.Mu.Unlock()
						if canAdd {
							newCon = append(newCon, c)
						} else {
							_, ok := c.ConnManagerInterface.(net.PacketConn)
							if ok {
								print("")
							}
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

var (
	MixedMaxCount = 60
	TCPMaxCount   = 40
)

type GoroutineLimiter[T any] struct {
	once      sync.Once
	valueChan chan T
	handle    func(T)
	maxCount  int
}

func CreateGoroutineLimiter[T any](maxCount int, handle func(T)) *GoroutineLimiter[T] {
	return &GoroutineLimiter[T]{
		valueChan: make(chan T, 1),
		handle:    handle,
		maxCount:  maxCount,
	}
}

func (v *GoroutineLimiter[T]) SetMaxCount(count int) {
	v.maxCount = count
}
func (v *GoroutineLimiter[T]) queueHandle() {

	countChan := make(chan int, v.maxCount)

	for {
		cs := <-v.valueChan

		go func(cs T) {
			v.handle(cs)
			<-countChan
		}(cs)
		countChan <- 1
	}
}

func (v *GoroutineLimiter[T]) HandleValue(value T) {

	v.valueChan <- value
	v.once.Do(func() {
		go v.queueHandle()
	})

}
