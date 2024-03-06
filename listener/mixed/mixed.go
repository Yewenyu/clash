package mixed

import (
	"net"

	"github.com/Dreamacro/clash/common/cache"
	connmanager "github.com/Dreamacro/clash/common/connManager"
	N "github.com/Dreamacro/clash/common/net"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/listener/http"
	"github.com/Dreamacro/clash/listener/socks"
	"github.com/Dreamacro/clash/transport/socks4"
	"github.com/Dreamacro/clash/transport/socks5"
)

type Listener struct {
	listener net.Listener
	addr     string
	cache    *cache.LruCache
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

	ml := &Listener{
		listener: l,
		addr:     addr,
		cache:    cache.New(cache.WithAge(30)),
	}
	goLimiter = connmanager.CreateGoroutineLimiter(connmanager.MixedMaxCount, func(mht mixHandleType) {

		handleConn1(mht.conn, mht.in, mht.cache)
	})
	go func() {
		for {
			c, err := ml.listener.Accept()
			if err != nil {
				if ml.closed {
					break
				}
				continue
			}
			handleConn(c, in, ml.cache)
		}
	}()

	return ml, nil
}

type mixHandleType struct {
	conn  net.Conn
	in    chan<- C.ConnContext
	cache *cache.LruCache
}

var goLimiter *connmanager.GoroutineLimiter[mixHandleType]

func handleConn(conn net.Conn, in chan<- C.ConnContext, cache *cache.LruCache) {
	goLimiter.HandleValue(mixHandleType{conn: conn, in: in, cache: cache})
}
func handleConn1(conn net.Conn, in chan<- C.ConnContext, cache *cache.LruCache) {
	conn.(*net.TCPConn).SetKeepAlive(true)
	bufConn := N.NewBufferedConn(conn)
	head, err := bufConn.Peek(1)
	if err != nil {
		return
	}

	switch head[0] {
	case socks4.Version:
		socks.HandleSocks4(bufConn, in)
	case socks5.Version:
		socks.HandleSocks5(bufConn, in)
	default:

		http.HandleConn(bufConn, in, cache)
	}
}
