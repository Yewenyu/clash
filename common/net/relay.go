package net

import (
	"io"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/Dreamacro/clash/log"
	gl "github.com/Yewenyu/GoLimiter"
)

var (
	TcpTimeout  = 5
	UdpTimeOut  = 5
	HttpTimeout = 5
	DNSTimeout  = 5

	queuePool = NewQueuePool(60)
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

func Relay(leftConn, rightConn net.Conn, useHttpTimeout bool, useDNSTimeout bool) {
	queuePool.addConns(&ConnsInfo{
		leftConn:       leftConn,
		rightConn:      rightConn,
		useHttpTimeout: useHttpTimeout,
		useDNSTimeout:  useDNSTimeout,
		key:            leftConn.RemoteAddr().String() + rightConn.RemoteAddr().String(),
		activeTime:     time.Now(),
	})
}
func Relay2(leftConn, rightConn net.Conn, useHttpTimeout bool, useDNSTimeout bool) {
	if TCPBufferSize == 0 {
		Relay1(leftConn, rightConn)
		return
	}
	// handle(cChan, &handleO)
	// 如果两个连接在指定时间内有数据交互，则继续连接，否则关闭连接
	timeout := TcpTimeout
	if useHttpTimeout {
		timeout = HttpTimeout
	}
	if useDNSTimeout {
		timeout = DNSTimeout
	}
	timeLock := time.NewTimer(time.Duration(timeout) * time.Second)

	handle := func(w, r net.Conn) {
		b := pool.Get().([]byte)
		defer pool.Put(b)
		defer w.Close()
	loop:
		for {
			select {
			case <-timeLock.C:
				break loop
			default:
				// 更新连接时间
				timeLock.Reset(time.Duration(timeout) * time.Second)
				r.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
				n, err := r.Read(b)
				if err != nil {
					// 如果读取超时，继续循环
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					break loop
				}
				_, err = w.Write(b[:n])
				if err != nil {
					break loop
				}
			}

		}
	}
	go handle(rightConn, leftConn)
	handle(leftConn, rightConn)
	rightConn.SetReadDeadline(time.Now())
}

type ConnsInfo struct {
	leftConn, rightConn           net.Conn
	useHttpTimeout, useDNSTimeout bool
	key                           string
	activeTime                    time.Time
	lock                          sync.Mutex
}

type RunPool struct {
	connsInfos   map[string]*ConnsInfo
	lock         sync.Mutex
	limit        *gl.GoroutinePool[*ConnsInfo]
	maxConnCount int
}
type QueuePool struct {
	lock           sync.Mutex
	runPool        *RunPool
	maxConnCount   int
	currentCount   int
	onOnce         sync.Once
	connChan       chan *ConnsInfo
	connLevel1Chan chan *ConnsInfo
	connLevel2Chan chan *ConnsInfo
}

func NewQueuePool(maxConnCount int) *QueuePool {

	limitCount := maxConnCount / 3
	runPool := &RunPool{
		connsInfos:   make(map[string]*ConnsInfo),
		maxConnCount: limitCount,
	}
	runPool.limit = gl.NewGoroutinePool(limitCount, func(v *ConnsInfo) {
		runPool.handle(v)
	})
	queuePool := &QueuePool{
		maxConnCount:   maxConnCount,
		connChan:       make(chan *ConnsInfo, limitCount),
		connLevel1Chan: make(chan *ConnsInfo, limitCount),
		connLevel2Chan: make(chan *ConnsInfo, limitCount),
		runPool:        runPool,
	}
	return queuePool

}

func (p *QueuePool) addConns(connsInfo *ConnsInfo) {
	chanLevel := 0
	p.lock.Lock()
	if p.currentCount > p.maxConnCount/3*2 {
		p.stopRunPoolConn(p.runPool.maxConnCount, 0)
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
	default:
		p.connChan <- connsInfo
	}

	p.onOnce.Do(func() {
		go func() {
			for {

				var connsInfo *ConnsInfo
				select {
				case connsInfo = <-p.connLevel2Chan:
				case connsInfo = <-p.connLevel1Chan:
				case connsInfo = <-p.connChan:
				}
				p.runPool.limit.SubmitTask(connsInfo)
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
	//停止活跃度低的连接

	stopCons := []*ConnsInfo{}
	for _, connsInfo := range p.connsInfos {
		stopCons = append(stopCons, connsInfo)
	}

	if len(stopCons) > count {
		conns := stopCons
		sort.Slice(conns, func(i, j int) bool {
			return conns[i].activeTime.Before(conns[j].activeTime)
		})
		stopCons = conns[:count]
	}
	for _, connsInfo := range stopCons {
		if timeout > 0 {
			connsInfo.leftConn.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
			connsInfo.rightConn.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
		} else {
			connsInfo.leftConn.Close()
			connsInfo.rightConn.Close()
			delete(p.connsInfos, connsInfo.key)
		}
	}
	p.lock.Unlock()

}
func (p *RunPool) handle(connInfo *ConnsInfo) {

	p.lock.Lock()
	p.connsInfos[connInfo.key] = connInfo
	p.lock.Unlock()

	p.relay(connInfo)

	p.lock.Lock()
	delete(p.connsInfos, connInfo.key)
	p.lock.Unlock()
}

func (p *RunPool) relay(connInfo *ConnsInfo) {

	leftConn := connInfo.leftConn
	rightConn := connInfo.rightConn
	useHttpTimeout := connInfo.useHttpTimeout
	useDNSTimeout := connInfo.useDNSTimeout
	// handle(cChan, &handleO)
	// 如果两个连接在指定时间内有数据交互，则继续连接，否则关闭连接
	timeout := TcpTimeout
	if useHttpTimeout {
		timeout = HttpTimeout
	}
	if useDNSTimeout {
		timeout = DNSTimeout
	}

	handle := func(w, r net.Conn) {
		b := pool.Get().([]byte)
		defer pool.Put(b)
		defer w.Close()
	loop:
		for {
			connInfo.lock.Lock()
			connInfo.activeTime = time.Now()
			connInfo.lock.Unlock()
			// 更新连接时间

			r.SetReadDeadline(time.Now().Add(time.Duration(timeout) * time.Second))
			n, err := r.Read(b)
			if err != nil {

				log.Debugln("Relay error: %v", err)
				break loop
			}
			_, err = w.Write(b[:n])
			if err != nil {
				break loop
			}

		}
	}
	go handle(rightConn, leftConn)
	handle(leftConn, rightConn)
	rightConn.SetReadDeadline(time.Now())
}
