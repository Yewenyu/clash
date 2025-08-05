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
		conn.SetDeadline(time.Now().Add(time.Second * time.Duration(N.UdpTimeOut)))
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
