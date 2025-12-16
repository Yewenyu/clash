package dnstunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Dreamacro/clash/component/resolver"
	"github.com/Dreamacro/clash/log"
	"github.com/alitto/pond/v2"
	"github.com/miekg/dns"
)

// UDPResolver 支持多服务器的UDP DNS解析器（实现Resolver接口）
type UDPResolver struct {
	connections   []*UDPConnection // 每个服务器对应的UDP连接
	serverAddrs   []string         // DNS服务器地址列表（格式：ip:port）
	timeout       time.Duration    // 单次查询超时时间
	roundRobinIdx int              // 轮询策略当前索引
	socks5Addr    string           // SOCKS5代理地址（可选）
	dnsGoPool     pond.Pool        // 用于并发DNS查询的goroutine池
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
	//判断域名长度
	if len(host) > 253 {
		return nil, errors.New("域名长度超过限制")
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)
	resp, err := r.exchangeWithStrategy(ctx, msg)
	if err != nil || resp == nil {
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
		socks5Addr:    cfg.SOCKS5Proxy,
		dnsGoPool:     pond.NewPool(20),
	}, nil
}

func (r *UDPResolver) exchangeWithStrategy(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	return r.concurrentExchange(ctx, m)
}

type DNSCacheInfo struct {
	msg        *dns.Msg
	ttl        int64
	isQuerying bool
	waitQuery  chan chan *DNSCacheInfo
}

var dnsCach = make(map[string]*DNSCacheInfo)
var dnsLock sync.Mutex
var lastQueyTime = time.Now().Unix()

func (r *UDPResolver) concurrentExchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	dnsLock.Lock()
	dnsC, ok := dnsCach[m.Question[0].Name]
	if len(dnsCach) > 100 || time.Now().Unix()-lastQueyTime > 300 {
		// 按ttl排序
		type kv struct {
			Key   string
			Value *DNSCacheInfo
		}
		var ss []kv
		for k, v := range dnsCach {
			ss = append(ss, kv{k, v})
		}
		sort.Slice(ss, func(i, j int) bool {
			return ss[i].Value.ttl < ss[j].Value.ttl
		})
		for i := 0; i < 10 && i < len(ss)/2; i++ {
			delete(dnsCach, ss[i].Key)
		}
	}
	lastQueyTime = time.Now().Unix()

	dnsLock.Unlock()
	if ok {
		if dnsC.isQuerying {
			waitQuery := make(chan *DNSCacheInfo)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("所有服务器查询超时: %v", ctx.Err())
			case dnsC.waitQuery <- waitQuery:
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("所有服务器查询超时: %v", ctx.Err())
				case newC := <-waitQuery:
					return newC.msg, nil
				}
			}

		} else {
			return dnsC.msg, nil
		}
	} else {
		dnsLock.Lock()
		dnsC = &DNSCacheInfo{
			isQuerying: true,
			waitQuery:  make(chan chan *DNSCacheInfo),
		}
		dnsCach[m.Question[0].Name] = dnsC
		dnsLock.Unlock()
		if strings.Contains(m.Question[0].Name, "bilivideo.com") {
			_ = m
		}
		//dns递归查询
		m.RecursionDesired = true
		resp, err := r.query(ctx, m)
		if err != nil {
			return nil, err
		}
		dnsLock.Lock()
		dnsC.isQuerying = false
		if err == nil && resp != nil && len(resp.Answer) > 0 {
			dnsC.msg = resp
			dnsC.ttl = time.Now().Unix() + int64(resp.Answer[0].Header().Ttl)
			dnsCach[m.Question[0].Name] = dnsC

			r.dnsGoPool.Submit(func() {
			loop:
				for {
					select {
					case <-time.After(5 * time.Second):
						break loop
					case c := <-dnsC.waitQuery:
						c <- dnsC

					}
				}

			})
		} else {
			delete(dnsCach, m.Question[0].Name)
		}
		dnsLock.Unlock()
		return resp, err

	}

}

func (r *UDPResolver) defaultQuery(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	ipAddrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	} else if len(ipAddrs) == 0 {
		return nil, fmt.Errorf("%w: %s", "not found", host)
	}
	return ipAddrs, nil
}
func (r *UDPResolver) tcpQuery(host, dnsAddr string, useProxy bool) (*dns.Msg, error) {
	query := new(dns.Msg)
	query.SetQuestion(host, dns.TypeA)
	queryBytes, _ := query.Pack()
	socksAddr := ""
	if useProxy {
		socksAddr = r.socks5Addr
	}
	bytes, err := handleTCPDNS(socksAddr, dnsAddr, queryBytes)
	if err != nil {
		return nil, err
	}
	msg := new(dns.Msg)
	if err := msg.Unpack(bytes); err != nil {
		return nil, err
	}
	return msg, nil
}
func (r *UDPResolver) dohQuery(host, dnsAddr string, useProxy bool) (*dns.Msg, error) {
	query := new(dns.Msg)
	query.SetQuestion(host, dns.TypeA)
	queryBytes, _ := query.Pack()
	socksAddr := ""
	if useProxy {
		socksAddr = r.socks5Addr
	}
	bytes, err := doDNSQuery(socksAddr, dnsAddr, queryBytes)
	if err != nil {
		return nil, err
	}
	msg := new(dns.Msg)
	if err := msg.Unpack(bytes); err != nil {
		return nil, err
	}
	return msg, nil
}

func (r *UDPResolver) query(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	respChan := make(chan *dns.Msg, len(r.connections))
	errChan := make(chan error, len(r.connections))
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	handleDNSBytes := func(bytes []byte, dnsAddr string) (*dns.Msg, error) {
		newMsg := new(dns.Msg)
		if err := newMsg.Unpack(bytes); err != nil {
			return nil, err
		}

		if len(newMsg.Answer) == 0 && len(newMsg.Ns) > 0 {
			host := newMsg.Ns[0].Header().Name
			result, err := r.tcpQuery(host, dnsAddr, true)
			if err != nil {
				return nil, err
			}
			if len(result.Answer) == 0 {
				return nil, fmt.Errorf("tcp查询失败: %v", err)
			}

			return result, nil

		}
		return newMsg, nil
	}

	for i, conn := range r.connections {
		r.dnsGoPool.Submit(func() {
			bytes, _ := m.Pack()
			dnsAddr := r.serverAddrs[i]
			info := &DNSInfo{
				remoteAddr: dnsAddr,
				bytes:      bytes,
				err:        nil,
				handle: func(resp *DNSInfo) {
					newMsg, err := handleDNSBytes(resp.bytes, dnsAddr)
					if err != nil {
						errChan <- err
						return
					}
					respChan <- newMsg
				},
			}
			conn.sendChan <- info

		})

		// go func(dnsAddr string) {
		// 	queryByts, _ := m.Pack()
		// 	resp, err := handleTCPDNS(r.socks5Addr, dnsAddr, queryByts)
		// 	if err != nil {
		// 		return
		// 	}
		// 	newMsg := handleDNSBytes(resp, true)
		// 	if newMsg == nil {
		// 		return
		// 	}
		// 	respChan <- newMsg
		// }(conn.remoteAddr)
	}
	errCount := 0
	lastHandle := func() *dns.Msg {
		result, _ := r.tcpQuery(m.Question[0].Name, r.serverAddrs[0], false)
		return result
	}
	for {
		select {
		case <-ctx.Done():
			result := lastHandle()
			if result != nil {
				return result, nil
			}
			return nil, fmt.Errorf("所有服务器查询超时: %v", ctx.Err())
		case err := <-errChan:
			errCount++
			if errCount == len(r.connections) {
				result := lastHandle()
				if result != nil {
					return result, nil
				}
				return nil, fmt.Errorf("所有服务器查询失败: %v", err)
			}
		case resp := <-respChan:

			return resp, nil
		}
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
