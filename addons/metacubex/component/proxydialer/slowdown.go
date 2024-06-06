package proxydialer

import (
	"context"
	"net"
	"net/netip"

	"github.com/Dreamacro/clash/addons/metacubex/component/slowdown"
	D "github.com/Dreamacro/clash/component/dialer"
)

type SlowDownDialer struct {
	Dialer   D.IDialer
	Slowdown *slowdown.SlowDown
}

func (d SlowDownDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return slowdown.Do(d.Slowdown, ctx, func() (net.Conn, error) {
		return d.Dialer.DialContext(ctx, network, address)
	})
}

func (d SlowDownDialer) ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort) (net.PacketConn, error) {
	return slowdown.Do(d.Slowdown, ctx, func() (net.PacketConn, error) {
		return d.Dialer.ListenPacket(ctx, network, address, rAddrPort)
	})
}

func NewSlowDownDialer(d D.IDialer, sd *slowdown.SlowDown) SlowDownDialer {
	return SlowDownDialer{
		Dialer:   d,
		Slowdown: sd,
	}
}
