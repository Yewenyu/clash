package dialer

import (
	"context"
	"net"
	"net/netip"
)

type Dialer struct {
	Opt option
}

type IDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort) (net.PacketConn, error)
}

func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return DialContext(ctx, network, address, WithOption(d.Opt))
}

func (d Dialer) ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort) (net.PacketConn, error) {
	opt := WithOption(d.Opt)
	if rAddrPort.Addr().Unmap().IsLoopback() {
		// avoid "The requested address is not valid in its context."
		opt = WithInterface("")
	}
	fullAddr := rAddrPort.String()
	return ListenPacket(ctx, ParseNetwork(network, rAddrPort.Addr()), fullAddr, opt)
}

func NewDialer(options ...Option) Dialer {
	opt := applyOptions(options...)
	return Dialer{Opt: *opt}
}

func ParseNetwork(network string, addr netip.Addr) string {
	return network
}

func WithOption(o option) Option {
	return func(opt *option) {
		*opt = o
	}
}

func applyOptions(options ...Option) *option {
	opt := &option{
		interfaceName: DefaultInterface.Load(),
		routingMark:   int(DefaultRoutingMark.Load()),
	}

	for _, o := range DefaultOptions {
		o(opt)
	}

	for _, o := range options {
		o(opt)
	}

	return opt
}
