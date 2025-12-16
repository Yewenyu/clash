package net

import (
	"net"
	"sort"
	"sync"
	"time"

	connmanager "github.com/Dreamacro/clash/common/connManager"
	gopool "github.com/Dreamacro/clash/goPool"
	"github.com/alitto/pond/v2"
)

var (
	TcpTimeout  = 5
	UdpTimeOut  = 5
	HttpTimeout = 5
	DNSTimeout  = 5

	queuePool *QueuePool
	lock      sync.Mutex
)

// Relay copies between left and right bidirectionally.

var TCPBufferSize = 0

func Relay(leftConn, rightConn net.Conn, useHttpTimeout bool, useDNSTimeout bool) {
	lock.Lock()
	if queuePool == nil {
		queuePool = NewQueuePool(connmanager.TCPMaxCount)
	}
	lock.Unlock()
	timeout := TcpTimeout
	if useHttpTimeout {
		timeout = HttpTimeout
	}
	if useDNSTimeout {
		timeout = DNSTimeout
	}
	info := &ConnsInfo{
		LeftConn:   leftConn,
		RightConn:  rightConn,
		timeout:    timeout,
		isDns:      useDNSTimeout,
		key:        leftConn.RemoteAddr().String() + rightConn.RemoteAddr().String(),
		activeTime: time.Now(),
	}
	queuePool.AddConns(info)
}

type ConnsInterface interface {
	Relay()
	Key() string
	ActiveTime() time.Time
	SetActiveTime(time time.Time)
	Timeout() int
	SetTimeout(timeout int)
	IsDns() bool
	Lock() *sync.Mutex
	IsStop() bool
	Close()
	GetGoPool() pond.Pool
}
type ConnsInfo struct {
	LeftConn, RightConn net.Conn
	key                 string
	activeTime          time.Time
	lock                sync.Mutex
	timeout             int
	isDns               bool
}

func (c *ConnsInfo) Key() string {
	return c.key
}
func (c *ConnsInfo) ActiveTime() time.Time {
	return c.activeTime
}
func (c *ConnsInfo) Lock() *sync.Mutex {
	return &c.lock
}
func (c *ConnsInfo) Timeout() int {
	return c.timeout
}

func (c *ConnsInfo) SetTimeout(timeout int) {
	c.timeout = timeout
	c.LeftConn.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
	c.RightConn.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
}

func (c *ConnsInfo) IsDns() bool {
	return c.isDns
}
func (c *ConnsInfo) IsStop() bool {
	return c.LeftConn == nil || c.RightConn == nil
}
func (c *ConnsInfo) Close() {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.LeftConn != nil {
		c.LeftConn.Close()
		c.LeftConn = nil
	}
	if c.RightConn != nil {
		c.RightConn.Close()
		c.RightConn = nil
	}
}
func (c *ConnsInfo) GetGoPool() pond.Pool {
	return gopool.Go
}
func (c *ConnsInfo) SetActiveTime(time time.Time) {
	c.activeTime = time
}

func (connInfo *ConnsInfo) Relay() {
	leftConn := connInfo.LeftConn
	rightConn := connInfo.RightConn

	defer func() {
		rightConn.SetReadDeadline(time.Now())
		connInfo.Close()
		// runtime.GC()
	}()
	buf := make([]byte, 10)
	n, err := leftConn.Read(buf)
	if err != nil {
		connInfo.Close()
		return
	}
	rightConn.Write(buf[:n])
	handle := func(w, r net.Conn) {
		b := make([]byte, TCPBufferSize)
	loop:
		for {
			connInfo.lock.Lock()
			connInfo.activeTime = time.Now()
			timeout := connInfo.Timeout()
			connInfo.lock.Unlock()
			// 更新连接时间

			r.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
			n, err := r.Read(b)
			if err != nil {
				break loop
			}
			_, err = w.Write(b[:n])
			if err != nil {
				break loop
			}

		}
	}

	gopool.Go.Submit(func() {
		handle(rightConn, leftConn)
	})
	handle(leftConn, rightConn)
}

type RunPool struct {
	connsInfos   map[string]ConnsInterface
	lock         sync.Mutex
	limit        *gopool.GoroutinePool[ConnsInterface]
	maxConnCount int
}
type QueuePool struct {
	lock           sync.Mutex
	runPool        *RunPool
	maxConnCount   int
	currentCount   int
	onOnce         sync.Once
	connChan       chan ConnsInterface
	connLevel1Chan chan ConnsInterface
	connLevel2Chan chan ConnsInterface
	connLevel3Chan chan ConnsInterface
}

func NewQueuePool(maxConnCount int) *QueuePool {

	limitCount := maxConnCount / 3
	runPool := &RunPool{
		connsInfos:   make(map[string]ConnsInterface),
		maxConnCount: limitCount,
	}
	runPool.limit = gopool.NewGoroutinePool(limitCount, func(v ConnsInterface) {
		runPool.handle(v)
	})
	queuePool := &QueuePool{
		maxConnCount:   maxConnCount,
		connChan:       make(chan ConnsInterface, limitCount),
		connLevel1Chan: make(chan ConnsInterface, limitCount),
		connLevel2Chan: make(chan ConnsInterface, limitCount),
		connLevel3Chan: make(chan ConnsInterface, limitCount),
		runPool:        runPool,
	}
	return queuePool

}

func (p *QueuePool) AddConns(connsInfo ConnsInterface) {
	chanLevel := 0
	p.lock.Lock()
	if p.currentCount > p.maxConnCount {
		p.stopRunPoolConn(p.runPool.maxConnCount, 0)
		chanLevel = 3
	} else if p.currentCount > p.maxConnCount/3*2 {
		p.stopRunPoolConn(p.runPool.maxConnCount, 1)
		chanLevel = 2
	} else if p.currentCount > p.maxConnCount/3*1 {
		p.stopRunPoolConn(p.runPool.maxConnCount/3, 2)
		chanLevel = 1
	}
	p.currentCount++
	p.lock.Unlock()

	switch chanLevel {
	case 1:
		p.connLevel1Chan <- connsInfo
	case 2:
		p.connLevel2Chan <- connsInfo
	case 3:
		p.connLevel3Chan <- connsInfo
	default:
		p.connChan <- connsInfo
	}

	p.onOnce.Do(func() {
		go func() {
			for {

				var connsInfo ConnsInterface
				select {
				case connsInfo = <-p.connLevel3Chan:
				case connsInfo = <-p.connLevel2Chan:
				case connsInfo = <-p.connLevel1Chan:
				case connsInfo = <-p.connChan:
				}
				connsInfo.GetGoPool().Submit(func() {
					p.runPool.relay(connsInfo)
				})
				p.lock.Lock()
				p.currentCount--
				p.lock.Unlock()
			}
		}()

	})

}

func (p *QueuePool) stopRunPoolConn(count, timeout int) {
	p.runPool.stopConns(count, timeout)
}

func (p *RunPool) stopConns(count, timeout int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	//停止活跃度低的连接

	if len(p.connsInfos) < count {
		return
	}

	stopCons := []ConnsInterface{}
	for _, connsInfo := range p.connsInfos {
		stopCons = append(stopCons, connsInfo)
	}

	if len(stopCons) > count {
		conns := stopCons
		sort.Slice(conns, func(i, j int) bool {
			return conns[i].ActiveTime().Before(conns[j].ActiveTime())
		})
		stopCons = conns[:count]
	}
	for _, connsInfo := range stopCons {
		if connsInfo.IsDns() {
			continue
		}
		delete(p.connsInfos, connsInfo.Key())
		lock := connsInfo.Lock()
		lock.Lock()
		defer lock.Unlock()
		if connsInfo.IsStop() {
			continue
		}
		if timeout == 0 {
			connsInfo.Close()
			continue
		}

		connsInfo.SetTimeout(timeout)
	}

}
func (p *RunPool) handle(connInfo ConnsInterface) {

	p.lock.Lock()
	p.connsInfos[connInfo.Key()] = connInfo
	p.lock.Unlock()

	p.relay(connInfo)

	p.lock.Lock()
	delete(p.connsInfos, connInfo.Key())
	p.lock.Unlock()
}

func (p *RunPool) relay(connInfo ConnsInterface) {

	connInfo.Relay()

}

// func Relay1(leftConn, rightConn net.Conn) {
// 	ch := make(chan error)

// 	go func() {
// 		// Wrapping to avoid using *net.TCPConn.(ReadFrom)
// 		// See also https://github.com/Dreamacro/clash/pull/1209
// 		_, err := io.Copy(WriteOnlyWriter{Writer: leftConn}, ReadOnlyReader{Reader: rightConn})
// 		leftConn.SetReadDeadline(time.Now())
// 		ch <- err
// 	}()

// 	io.Copy(WriteOnlyWriter{Writer: rightConn}, ReadOnlyReader{Reader: leftConn})
// 	rightConn.SetReadDeadline(time.Now())
// 	<-ch
// }
// func Relay2(leftConn, rightConn net.Conn, useHttpTimeout bool, useDNSTimeout bool) {
// 	if TCPBufferSize == 0 {
// 		Relay1(leftConn, rightConn)
// 		return
// 	}
// 	// handle(cChan, &handleO)
// 	// 如果两个连接在指定时间内有数据交互，则继续连接，否则关闭连接
// 	timeout := TcpTimeout
// 	if useHttpTimeout {
// 		timeout = HttpTimeout
// 	}
// 	if useDNSTimeout {
// 		timeout = DNSTimeout
// 	}
// 	timeLock := time.NewTimer(time.Duration(timeout) * time.Second)

// 	handle := func(w, r net.Conn) {
// 		b := make([]byte, TCPBufferSize)
// 		defer w.Close()
// 	loop:
// 		for {
// 			select {
// 			case <-timeLock.C:
// 				break loop
// 			default:
// 				// 更新连接时间
// 				timeLock.Reset(time.Duration(timeout) * time.Second)
// 				r.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
// 				n, err := r.Read(b)
// 				if err != nil {
// 					// 如果读取超时，继续循环
// 					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
// 						continue
// 					}
// 					break loop
// 				}
// 				_, err = w.Write(b[:n])
// 				if err != nil {
// 					break loop
// 				}
// 			}

// 		}
// 	}
// 	go handle(rightConn, leftConn)
// 	handle(leftConn, rightConn)
// 	rightConn.SetReadDeadline(time.Now())
// }
