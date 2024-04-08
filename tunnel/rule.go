package tunnel

import (
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"time"

	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	R "github.com/Dreamacro/clash/rule"
	"github.com/Dreamacro/clash/transport/socks5"
	"github.com/miekg/dns"
	"golang.org/x/net/proxy"
)

var (
	DnsCachSize = 100
)

type TRule struct {
	Rules     []C.Rule
	domainMap map[string]int
	dnsMap    map[string]string
	dnsCach   map[string]DNSCach
	l         sync.Mutex
}
type DNSCach struct {
	msg  dns.Msg
	time int64
	name string
}

func CreateTRule(rules []C.Rule) *TRule {
	tr := &TRule{}
	tr.Rules = rules
	tr.domainMap = make(map[string]int)
	tr.dnsMap = make(map[string]string)
	tr.dnsCach = make(map[string]DNSCach)
	return tr
}

func (r *TRule) MatchCRule(meta *C.Metadata) int {
	for i, v := range r.Rules {
		if v.Match(meta) {
			return i
		}
	}
	return -1
}
func (r *TRule) Match(meta *C.Metadata) (int, bool) {
	go testUDP()

	r.l.Lock()
	defer r.l.Unlock()
	index, exist := -1, false
	if meta.Host != "" {
		index, exist = r.domainMap[meta.Host]
	}
	handleMap := func() (int, bool) {
		index := r.MatchCRule(meta)
		if index != -1 {
			if index != len(r.Rules)-1 {
				var v = meta.Host
				if v == "" {
					v = meta.DstIP.String()
				}
				r.domainMap[v] = index
			}
			return index, true
		}
		return index, false
	}
	if !exist {
		index, exist = r.domainMap[meta.DstIP.String()]
		if !exist {
			host, exist := r.dnsMap[meta.DstIP.String()]
			log.Debugln("[TRULE] dnsMap %s --> %s", meta.DstIP.String(), host)
			if exist {
				index, exist = r.domainMap[host]
				if !exist {
					handle := func() {}
					if meta.Host == "" {
						meta.Host = host
						handle = func() {
							meta.Host = ""
						}
					}
					_, _ = handleMap()
					handle()
					index, exist = r.domainMap[host]
				}
				if exist {
					hRule := r.Rules[index]

					payload := meta.DstIP.String()
					if meta.DstIP.To4() == nil {
						payload += "/128"
					} else {
						payload += "/32"
					}
					ipRule, err := R.ParseRule("IP-CIDR", payload, hRule.Adapter(), nil)
					if err == nil {
						index = len(r.Rules) - 1
						last := r.Rules[index]
						r.domainMap[meta.DstIP.String()] = index
						r.Rules = append(r.Rules[:index], ipRule)
						r.Rules = append(r.Rules, last)

						return index, true
					}
				}
			}
		}
	}
	if !exist {
		return handleMap()
	}
	return index, exist

}

func (r *TRule) HandleDns(bytes []byte) {
	err := dns.IsMsg(bytes)
	if err != nil {
		return
	}
	msg := new(dns.Msg)
	_ = msg.Unpack(bytes)

	r.l.Lock()
	defer r.l.Unlock()
	var qName = ""
	for _, q := range msg.Question {
		log.Debugln("[DNS] Question %s ", q.Name)
		qName = trimLastDot(q.Name)
	}
	if len(msg.Answer) > 0 {
		if len(r.dnsCach) > DnsCachSize {
			caches := make([]DNSCach, 0)
			for _, v := range r.dnsCach {
				caches = append(caches, v)
			}
			sort.Slice(caches, func(i, j int) bool {
				return caches[i].time < caches[j].time
			})
			caches = caches[:len(caches)/2]
			for _, v := range caches {
				delete(r.dnsCach, v.name)
			}
		}
		r.dnsCach[qName] = DNSCach{msg: *msg, time: time.Now().UnixMilli(), name: qName}
	}

	for _, rr := range msg.Answer {
		switch v := rr.(type) {
		case *dns.A:
			r.dnsMap[v.A.String()] = qName
		case *dns.AAAA:
			r.dnsMap[v.AAAA.String()] = qName
		}
	}
	log.Debugln("[DNS] %s --> %s", qName, msg.Answer)

}
func (r *TRule) GetReponseDns(bytes []byte) []byte {
	r.l.Lock()
	defer r.l.Unlock()
	err := dns.IsMsg(bytes)
	if err != nil {
		return nil
	}
	qus := new(dns.Msg)
	_ = qus.Unpack(bytes)
	if len(qus.Question) > 0 {
		name := trimLastDot(qus.Question[0].Name)
		cach, exist := r.dnsCach[name]
		if exist {
			answer := cach.msg
			answer.SetReply(qus)
			log.Debugln("[DNS] R %s --> %s", name, answer.Answer)
			bytes, _ := answer.Pack()
			return bytes
		}

	}
	return nil
}

func ListenDNS(addr string) {

}

func test() {
	// SOCKS5 代理的地址
	socks5Addr := "127.0.0.1:7779"

	// 目标 DNS 服务器的地址
	dnsServerAddr := "1.1.1.1:53"

	// 创建到 SOCKS5 代理的拨号器
	dialer, err := proxy.SOCKS5("tcp", socks5Addr, nil, proxy.Direct)
	if err != nil {
		log.Debugln("Failed to create SOCKS5 dialer: %v", err)
		return
	}

	// 使用拨号器创建连接
	conn, err := dialer.Dial("tcp", dnsServerAddr)
	if err != nil {
		log.Debugln("Failed to dial DNS server via SOCKS5: %v", err)
		return
	}
	defer conn.Close()

	// 创建 DNS 客户端，并指定自定义的连接
	c := new(dns.Client)
	c.Net = "tcp" // 确保使用与 conn 匹配的网络类型

	// 构建 DNS 查询消息
	m := new(dns.Msg)
	m.SetQuestion("34.160.152.12.in-addr.arpa.", dns.TypePTR)

	// 将 net.Conn 包装为 *dns.Conn
	dnsConn := &dns.Conn{Conn: conn}

	// 通过代理发送 DNS 查询
	r, _, err := c.ExchangeWithConn(m, dnsConn)
	if err != nil {
		log.Debugln("DNS query failed: %v", err)
		return
	}

	log.Debugln("[DNS] test %s", r.Answer)

}

func testUDP() {
	// SOCKS5 代理的地址
	socks5Addr := "127.0.0.1:7779"

	// 目标 DNS 服务器的地址（通过UDP relay）
	dnsServerAddr := "1.1.1.1:53"

	// 构建 DNS 查询消息
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("ip111.cn"), dns.TypeA) // 修改为你要查询的域名和类型
	// 将 net.Conn 包装为 *dns.Conn
	b, err := m.Pack()
	if err != nil {
		log.Debugln("[DNS UDP]Failed to create dns : %v", err)
		return
	}
	b, err = handleSocks5Udp(socks5Addr, dnsServerAddr, b)

	if err != nil {
		log.Debugln("[DNS UDP]Failed to send dns : %v", err)
		return
	}
	// 发送DNS查询
	r := new(dns.Msg)
	r.Unpack(b)

	log.Debugln("[DNS UDP] Query result: %s", r)

}

// trimLastDot 移除字符串最后的点（如果存在）
func trimLastDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1] // 返回除了最后一个字符以外的所有字符
	}
	return s // 如果没有最后的点，直接返回原字符串
}

func handleSocks5Udp(proxyAddr string, remoteAddr string, bytes []byte) ([]byte, error) {
	// 连接到SOCKS5代理服务器
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 请求UDP关联
	addr, err := socks5.ClientHandshake(conn, socks5.ParseAddr(remoteAddr), socks5.CmdUDPAssociate, nil)
	if err != nil {
		return nil, err
	}

	fmt.Printf("UDP associate granted. Bound address: %s\n", addr.String())

	// 获取绑定的UDP地址
	boundUDPAddr := addr.UDPAddr()

	// 监听该UDP端口
	udpConn, err := net.DialUDP("udp", nil, boundUDPAddr)
	if err != nil {
		return nil, err
	}
	defer udpConn.Close()

	// 编码目标地址和负载数据
	packet, err := socks5.EncodeUDPPacket(socks5.ParseAddr(remoteAddr), bytes)
	if err != nil {
		return nil, err
	}

	// 发送数据包
	_, err = udpConn.Write(packet)
	if err != nil {
		return nil, err
	}

	// 接收UDP数据包
	buffer := make([]byte, 2048) // 大小应根据预期的数据包大小调整
	udpConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := udpConn.ReadFromUDP(buffer)
	if err != nil && err != io.EOF {
		return nil, err
	}

	// 解码收到的数据包
	_, payload, err := socks5.DecodeUDPPacket(buffer[:n])
	if err != nil {
		return nil, err
	}

	return payload, nil
}
