package socks

import (
	"io"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/Dreamacro/clash/adapter/inbound"
	connmanager "github.com/Dreamacro/clash/common/connManager"
	N "github.com/Dreamacro/clash/common/net"
	"github.com/Dreamacro/clash/common/pool"
	C "github.com/Dreamacro/clash/constant"
	authStore "github.com/Dreamacro/clash/listener/auth"
	"github.com/Dreamacro/clash/transport/socks4"
	"github.com/Dreamacro/clash/transport/socks5"
)

type Listener struct {
	listener net.Listener
	addr     string
	closed   bool
}

// RawAddress implements C.Listener
func (l *Listener) RawAddress() string {
	return l.addr
}

// Address implements C.Listener
func (l *Listener) Address() string {
	return l.listener.Addr().String()
}

// Close implements C.Listener
func (l *Listener) Close() error {
	l.closed = true
	return l.listener.Close()
}

func New(addr string, in chan<- C.ConnContext) (C.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	sl := &Listener{
		listener: l,
		addr:     addr,
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if sl.closed {
					break
				}
				continue
			}
			go handleSocks(c, in)
		}
	}()

	return sl, nil
}

func handleSocks(conn net.Conn, in chan<- C.ConnContext) {
	conn.(*net.TCPConn).SetKeepAlive(true)
	bufConn := N.NewBufferedConn(conn)
	head, err := bufConn.Peek(1)
	if err != nil {
		conn.Close()
		return
	}

	switch head[0] {
	case socks4.Version:
		HandleSocks4(bufConn, in)
	case socks5.Version:
		HandleSocks5(bufConn, in)
	default:
		conn.Close()
	}
}

func HandleSocks4(conn net.Conn, in chan<- C.ConnContext) {
	addr, _, err := socks4.ServerHandshake(conn, authStore.Authenticator())
	if err != nil {
		conn.Close()
		return
	}
	in <- inbound.NewSocket(socks5.ParseAddr(addr), conn, C.SOCKS4)
}

func HandleSocks5(conn net.Conn, in chan<- C.ConnContext) {
	target, command, _, err := socks5.ServerHandshake(conn, authStore.Authenticator())
	if err != nil {
		conn.Close()
		return
	}
	if command == socks5.CmdUDPAssociate {
		udpLock.Lock()
		if udpQueuePool == nil {
			udpQueuePool = N.NewQueuePool(connmanager.TCPMaxCount)
		}
		udpLock.Unlock()
		udpQueuePool.AddConns(&UdpConnsInfo{
			conn:       conn,
			timeout:    N.UdpTimeOut,
			key:        conn.LocalAddr().String(),
			activeTime: time.Now(),
		})
		return
	}
	in <- inbound.NewSocket(target, conn, C.SOCKS5)
}

var udpLock sync.Mutex
var udpQueuePool *N.QueuePool

type UdpConnsInfo struct {
	conn       net.Conn
	key        string
	activeTime time.Time
	timeout    int
	lock       sync.Mutex
}

func (u *UdpConnsInfo) Key() string {
	return u.key
}

func (u *UdpConnsInfo) SetActiveTime(time time.Time) {
	u.activeTime = time
}
func (u *UdpConnsInfo) IsDns() bool {
	return false
}
func (u *UdpConnsInfo) Lock() *sync.Mutex {
	return &u.lock
}

func (u *UdpConnsInfo) ActiveTime() time.Time {
	return u.activeTime
}

func (u *UdpConnsInfo) IsStop() bool {
	return u.conn == nil
}

func (u *UdpConnsInfo) Close() {
	u.conn.Close()
	u.conn = nil
}
func (u *UdpConnsInfo) Timeout() int {
	return u.timeout
}
func (u *UdpConnsInfo) SetTimeout(timeout int) {
	u.timeout = timeout
}

func (u *UdpConnsInfo) Relay() {
	buf := pool.Get(pool.UDPBufferSize)
	conn := u.conn
	defer pool.Put(buf)
	defer conn.Close()
	defer runtime.GC()

	for {
		u.lock.Lock()
		conn.SetDeadline(time.Now().Add(time.Second * time.Duration(u.timeout)))
		u.lock.Unlock()
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		u.lock.Lock()
		u.SetActiveTime(time.Now())
		u.lock.Unlock()
		io.Discard.Write(buf[:n])
	}
}

type ConnValue struct {
	conn   net.Conn
	target socks5.Addr
	in     chan<- C.ConnContext
}

// NewWaitQueue 构造函数：初始化所有chan，避免nil panic（核心修正1）
func NewWaitQueue(maxWaitCount int, handleConn func(ConnValue)) *WaitQueue {
	if maxWaitCount <= 0 {
		maxWaitCount = 100 // 默认最大等待数
	}
	wq := &WaitQueue{
		maxWaitCount: maxWaitCount,
		// 初始化双队列：缓冲容量=maxWaitCount，避免写入阻塞
		waitQueue: make(chan ConnValue, maxWaitCount),
		// 初始化signalChan：带缓冲，避免Add阻塞在signalChan写入
		signalChan: make(chan struct{}),
		queueChan:  make(chan chan ConnValue),
		clearChan:  make(chan chan ConnValue),
		handleConn: handleConn,
	}
	return wq
}

type WaitQueue struct {
	lock                 sync.Mutex
	waitQueue            chan ConnValue
	queueChan, clearChan chan chan ConnValue
	signalChan           chan struct{}
	oncdHandle           sync.Once
	maxWaitCount         int
	handleConn           func(ConnValue)
}

func (w *WaitQueue) Add(connValue ConnValue) {

	w.oncdHandle.Do(func() {

		go w.handle()
		w.queueChan <- w.waitQueue
	})

	w.lock.Lock()
	if len(w.waitQueue) == w.maxWaitCount {
		v := w.waitQueue
		w.waitQueue = make(chan ConnValue, w.maxWaitCount)
		w.signalChan <- struct{}{}
		go func(v chan ConnValue) {
			w.clearChan <- v
		}(v)
		w.queueChan <- w.waitQueue
	}
	w.waitQueue <- connValue
	w.lock.Unlock()

}
func (w *WaitQueue) handle() {
	for queue := range w.queueChan {

	signal:
		for {
			select {
			case <-w.signalChan:
				break signal
			case connValue := <-queue:
				w.handleConn(connValue)
			default:
				if len(w.queueChan) > 0 {
					break signal
				}
			}
		}
	}

	go func() {
		for v := range w.clearChan {
			for {
				select {
				case connValue := <-v:
					connValue.conn.Close()
				default:

				}
				if len(v) == 0 {
					close(v)
					break
				}
			}
			runtime.GC()
		}
	}()

}
