package dnstunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Dreamacro/clash/component/resolver"
	"github.com/Dreamacro/clash/log"
	"github.com/miekg/dns"
)

// UDPResolver 支持多服务器的UDP DNS解析器（实现Resolver接口）
type UDPResolver struct {
	connections   []*UDPConnection // 每个服务器对应的UDP连接
	serverAddrs   []string         // DNS服务器地址列表（格式：ip:port）
	timeout       time.Duration    // 单次查询超时时间
	roundRobinIdx int              // 轮询策略当前索引
}

// ResolverConfig 解析器配置
type ResolverConfig struct {
	SOCKS5Proxy string        // SOCKS5代理地址（可选）
	ServerAddrs []string      // DNS服务器地址列表（必填）
	Timeout     time.Duration // 单次查询超时时间（默认5秒）
}

// 新增：实现Resolver接口的所有方法
var _ resolver.Resolver = (*UDPResolver)(nil) // 确保编译时检查接口实现

// LookupIP 并发查询所有服务器，返回所有有效IP（IPv4+IPv6）（原有方法）
func (r *UDPResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	ipv4, err := r.LookupIPv4(ctx, host)
	if err != nil {
		return nil, err
	}
	ipv6, err := r.LookupIPv6(ctx, host)
	if err != nil {
		return nil, err
	}
	return append(ipv4, ipv6...), nil
}

// LookupIPv4 根据策略查询IPv4地址（A记录）（原有方法）
func (r *UDPResolver) LookupIPv4(ctx context.Context, host string) ([]net.IP, error) {
	//判断host是否ip
	if ip := net.ParseIP(host); ip != nil {
		if !strings.Contains(host, ":") {
			return []net.IP{ip}, nil
		}
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
	resp, err := r.exchangeWithStrategy(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("IPv4查询失败: %w", err)
	}
	return r.extractIPs(resp, dns.TypeA), nil
}

// LookupIPv6 根据策略查询IPv6地址（AAAA记录）（原有方法）
func (r *UDPResolver) LookupIPv6(ctx context.Context, host string) ([]net.IP, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(host), dns.TypeAAAA)
	resp, err := r.exchangeWithStrategy(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("IPv6查询失败: %w", err)
	}
	return r.extractIPs(resp, dns.TypeAAAA), nil
}

// 新增：Resolver接口要求的单次解析方法
func (r *UDPResolver) ResolveIP(host string) (net.IP, error) {
	return r.resolveIP(context.Background(), host)
}

func (r *UDPResolver) ResolveIPv4(host string) (net.IP, error) {
	return r.resolveIPv4(context.Background(), host)
}

func (r *UDPResolver) ResolveIPv6(host string) (net.IP, error) {
	return r.resolveIPv6(context.Background(), host)
}

// 新增：带上下文的解析方法（内部使用）
func (r *UDPResolver) resolveIP(ctx context.Context, host string) (net.IP, error) {
	ips, err := r.LookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("未找到IP地址")
	}
	return ips[0], nil
}

func (r *UDPResolver) resolveIPv4(ctx context.Context, host string) (net.IP, error) {
	ips, err := r.LookupIPv4(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("未找到IPv4地址")
	}
	return ips[0], nil
}

func (r *UDPResolver) resolveIPv6(ctx context.Context, host string) (net.IP, error) {
	ips, err := r.LookupIPv6(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("未找到IPv6地址")
	}
	return ips[0], nil
}

// 新增：实现Resolver接口的ExchangeContext方法
func (r *UDPResolver) ExchangeContext(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	return r.exchangeWithStrategy(ctx, m)
}

// 以下为原有方法（保持核心逻辑不变）

func SetupHttpDNSResolver(proxyAddr string, dnsServers []string, timeout int) *UDPResolver {
	cfg := ResolverConfig{
		SOCKS5Proxy: proxyAddr,
		ServerAddrs: dnsServers,
		Timeout:     time.Duration(timeout) * time.Second,
	}
	newResolver, err := NewUDPResolver(cfg)
	if err != nil {
		log.Errorln("DNS解析器初始化失败: %v", err)
	}
	resolver.DefaultResolver = newResolver
	return newResolver
}

func NewUDPResolver(cfg ResolverConfig) (*UDPResolver, error) {
	if len(cfg.ServerAddrs) == 0 {
		return nil, errors.New("必须提供至少一个DNS服务器地址")
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	recvChan := make(chan *DNSInfo, 10*len(cfg.ServerAddrs))
	connections := make([]*UDPConnection, 0, len(cfg.ServerAddrs))
	for _, addr := range cfg.ServerAddrs {
		conn, err := NewUDPConnection(cfg.SOCKS5Proxy, addr)
		if err != nil {
			return nil, fmt.Errorf("创建UDP连接失败（%s）: %w", addr, err)
		}
		conn.recvChan = recvChan
		connections = append(connections, conn)
	}
	go func() {
		for response := range recvChan {
			h, _ := response.handle.(func(info *DNSInfo))
			if h == nil {
				continue
			}
			h(response)
		}
	}()

	return &UDPResolver{
		connections:   connections,
		serverAddrs:   cfg.ServerAddrs,
		timeout:       cfg.Timeout,
		roundRobinIdx: 0,
	}, nil
}

func (r *UDPResolver) exchangeWithStrategy(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	return r.concurrentExchange(ctx, m)
}

func (r *UDPResolver) concurrentExchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	respChan := make(chan *dns.Msg, len(r.connections))
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	for i, conn := range r.connections {
		go func(idx int, connection *UDPConnection) {
			bytes, _ := m.Pack()
			info := &DNSInfo{
				remoteAddr: r.serverAddrs[idx],
				bytes:      bytes,
				err:        nil,
				handle: func(resp *DNSInfo) {
					newMsg := new(dns.Msg)
					if err := newMsg.Unpack(resp.bytes); err != nil {
						return
					}
					respChan <- newMsg
				},
			}
			connection.sendChan <- info

		}(i, conn)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("所有服务器查询超时: %v", ctx.Err())

	case resp := <-respChan:

		return resp, nil
	}
}

func (r *UDPResolver) extractIPs(resp *dns.Msg, qtype uint16) []net.IP {
	var ips []net.IP
	for _, ans := range resp.Answer {
		switch a := ans.(type) {
		case *dns.A:
			if qtype == dns.TypeA {
				ips = append(ips, a.A)
			}
		case *dns.AAAA:
			if qtype == dns.TypeAAAA {
				ips = append(ips, a.AAAA)
			}
		}
	}
	return ips
}

func (r *UDPResolver) Close() error {
	for _, conn := range r.connections {
		conn.Close()
	}
	return nil
}
