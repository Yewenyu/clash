package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	connmanager "github.com/Dreamacro/clash/common/connManager"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	R "github.com/Dreamacro/clash/rule"
	"github.com/Dreamacro/clash/transport/socks5"
	"github.com/miekg/dns"
	"golang.org/x/net/proxy"
)

var (
	DnsCachTime               = 120
	MaxDnsConnectCount        = 30
	DnsTimeout                = 3
	tRule              *TRule = CreateTRule(make([]C.Rule, 0))
)

type TRule struct {
	Rules     []C.Rule
	domainMap map[string]int
	dnsMap    map[string]string
	l         sync.Mutex
}

func CreateTRule(rules []C.Rule) *TRule {
	tr := &TRule{}
	tr.Rules = rules
	tr.domainMap = make(map[string]int)
	tr.dnsMap = make(map[string]string)
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

func (r *TRule) handleDns(bytes []byte) {
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
		log.Debugln("[DNS handle] Question %s ", q.Name)
		qName = trimLastDot(q.Name)
		break
	}

	for _, rr := range msg.Answer {
		switch v := rr.(type) {
		case *dns.A:
			r.dnsMap[v.A.String()] = qName
		case *dns.AAAA:
			r.dnsMap[v.AAAA.String()] = qName
		}
	}
	log.Debugln("[DNS handle] %s --> %s", qName, msg.Answer)

}
func dnsDir() string {
	homedir := C.Path.HomeDir()
	dnsDir := homedir + "/DNSCach"
	return dnsDir
}
func (r *TRule) dnsFilePath(name string) string {
	dnsDir := dnsDir() + "/" + name + ".d"
	return dnsDir
}
func (r *TRule) getReponseDns(bytes []byte) ([]byte, bool) {
	r.l.Lock()
	defer r.l.Unlock()
	err := dns.IsMsg(bytes)
	if err != nil {
		return nil, true
	}
	qus := new(dns.Msg)
	_ = qus.Unpack(bytes)
	if len(qus.Question) > 0 {
		name := fileName(qus)
		path := r.dnsFilePath(name)

		bytes, err := readBytesFromFile(path)
		if err == nil {
			answer := new(dns.Msg)
			err = answer.Unpack(bytes)
			if err != nil {
				return nil, true
			}

			answer = buildDNSResponseFromCache(qus, answer)
			if answer == nil || len(answer.Answer) == 0 {
				return nil, true
			}
			bytes, _ := answer.Pack()
			// 获取文件状态信息
			fileInfo, _ := os.Stat(path)

			if fileInfo != nil {
				// 获取并打印文件的修改时间
				modTime := fileInfo.ModTime()
				cTime := time.Now().Unix()
				mTime := modTime.Unix()
				if cTime-mTime > int64(DnsCachTime*2) {
					return nil, true
				}
				if cTime-mTime > int64(DnsCachTime) {
					// _ = os.Remove(path)
					return bytes, true
				}
			}
			log.Debugln("[DNS Cach Reponse] %s --> %s", name, answer.Answer)

			return bytes, false
		}

	}
	return nil, true
}

// 构建DNS响应，使用缓存的响应
func buildDNSResponseFromCache(req *dns.Msg, cache *dns.Msg) *dns.Msg {
	// 验证请求与缓存是否匹配
	if len(req.Question) == 0 || len(cache.Answer) == 0 {
		return nil // 无效请求或缓存响应
	}
	if req.Question[0].Name != cache.Answer[0].Header().Name || req.Question[0].Qtype != cache.Question[0].Qtype {
		return nil // 请求和缓存不匹配
	}

	// 可以选择修改缓存响应，例如更新TTL
	// for _, ans := range cache.Answer {
	// 	ans.Header().Ttl = 300 // 设置或重置TTL
	// }

	// 设置回复位
	cache.SetReply(req)
	return cache
}
func fileName(dns *dns.Msg) string {

	return fmt.Sprintf("%s_%d", dns.Question[0].Name, dns.Question[0].Qtype)
}
func (r *TRule) setDNSCach(bytes []byte, l *sync.Mutex) {

	msg := new(dns.Msg)
	err := msg.Unpack(bytes)
	if err != nil {
		return
	}

	var qName = fileName(msg)
	l.Lock()
	defer l.Unlock()
	if len(msg.Answer) > 0 {
		dnsDir := r.dnsFilePath(qName)
		oldCach, err := readBytesFromFile(dnsDir)
		if err == nil {
			omsg := new(dns.Msg)
			_ = omsg.Unpack(oldCach)
			answer := append(msg.Answer, omsg.Answer...)
			answer = RemoveDuplicates(answer, func(d dns.RR) string {
				n := d.Header().Name
				return n
			})
			msg.Answer = answer
			bytes, _ = msg.Pack()
		}

		err = writeFileEnsureDir(dnsDir, bytes)
		if err != nil {
			log.Errorln("[DNS Cach] file err : %v", err)
			return
		}

	}
}
func writeFileEnsureDir(filePath string, data []byte) error {
	// 确定文件的父目录
	dir := filepath.Dir(filePath)

	// 创建文件的父目录（如果不存在）
	err := os.MkdirAll(dir, 0755) // 使用0755权限确保目录可读写
	if err != nil {

		return fmt.Errorf("创建目录失败: %v", err)
	}

	// 创建或打开文件进行写入
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %v", err)
	}
	defer file.Close()

	// 写入数据
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}

	return nil
}
func readBytesFromFile(filePath string) ([]byte, error) {
	// ReadFile 直接读取文件内容到 []byte
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func getDirSize(dirPath string) (int64, error) {
	var size int64
	err := filepath.Walk(dirPath, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func removeOldestFiles(dirPath string) error {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	// 将文件按修改时间排序，最旧的文件在前
	sort.Slice(files, func(i, j int) bool {
		info, _ := files[i].Info()
		info2, _ := files[j].Info()
		return info.ModTime().Before(info2.ModTime())
	})

	// 删除一半最旧的文件
	for i := 0; i < len(files)/2; i++ {
		err := os.Remove(filepath.Join(dirPath, files[i].Name()))
		if err != nil {
			fmt.Printf("删除文件 %s 时出错: %v\n", files[i].Name(), err)
			// 可以选择在这里中止，或继续尝试删除其它文件
		} else {
			fmt.Printf("成功删除文件: %s\n", files[i].Name())
		}
	}

	return nil
}

func deleteDirSize(dirPath string, limit int64) {
	size, err := getDirSize(dirPath)
	if err != nil {
		return
	}
	if size > limit {
		err := removeOldestFiles(dirPath)
		if err != nil {
			fmt.Printf("删除文件时出错: %v\n", err)
		}
	}
}

func ListenDNS(localAddr, socks5Addr, mode string, cach bool, dnsAddrs []string, dohHost []string) {

	// 服务器监听的地址
	serverAddr := localAddr

	serverUDPAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		log.Fatalln("DNS Listener err: %s", err)
		return
	}

	conn, err := net.ListenUDP("udp", serverUDPAddr)
	if err != nil {
		log.Fatalln("DNS Listener err: %s", err)
		return
	}
	defer conn.Close()

	log.Infoln("DNS Listening at %s", serverAddr)

	buffer := make([]byte, 4096)

	if len(dnsAddrs) == 0 {
		dnsAddrs = []string{"8.8.8.8"}
	}
	type DNSV struct {
		oAddr *net.UDPAddr
		conn  *net.UDPConn
		bytes []byte
	}
	udpQuery := false
	tcpQuery := false
	dohQuery := false
	m := 0
	if strings.Contains(mode, "udp") {
		udpQuery = true
		m += 1
	}
	if strings.Contains(mode, "tcp") {
		tcpQuery = true
		m += 1
	}

	dnsCount := len(dnsAddrs) * m
	if strings.Contains(mode, "doh") {
		if len(dohHost) == 0 {
			dohHost = []string{"doh.opendns.com"}
		}
		dohQuery = true
	}
	dnsCount += len(dohHost)
	initTime := time.Now().Unix()
	dnsCanHandle := true
	var golimiter = connmanager.CreateGoroutineLimiter(MaxDnsConnectCount, func(v DNSV) {

		var l sync.Mutex
		rCount := 0
		rChan := make(chan []byte, dnsCount) // 使用buffered channel

		handle := func(bytes []byte, dnsAddr, mode string) {

			l.Lock()
			rCount += 1
			l.Unlock()

			defer func() {
				if len(bytes) == 0 {
					if rCount != dnsCount {
						return
					}
				}
				rChan <- bytes
			}()
			if bytes == nil {
				bytes = make([]byte, 0)
				return
			}
			msg := new(dns.Msg)
			_ = msg.Unpack(bytes)

			if len(msg.Answer) == 0 {
				log.Debugln("[DNS response %s] empty answer %s from %s", mode, msg.Question[0].Name, dnsAddr)
				return
			}
			log.Debugln("[DNS response %s] answer %s from %s", mode, msg.Answer, dnsAddr)
			if cach {
				tRule.setDNSCach(bytes, &l)
			}

		}
		dnsBytes := v.bytes
		r, canUpdate := tRule.getReponseDns(dnsBytes)
		if r != nil {
			_, _ = v.conn.WriteToUDP(r, v.oAddr)
			l.Lock()
			dnsCanHandle = time.Now().Unix()-initTime < int64(DnsCachTime)
			l.Unlock()
			if dnsCanHandle {
				tRule.handleDns(r)
			}
		}
		if canUpdate {
			h := func(addr, mode string, f func(socks5Addr string, addr string, dnsBytes []byte) ([]byte, error)) {
				bytes, err := f(socks5Addr, addr, dnsBytes)
				if err != nil {
					log.Debugln("%v", err)
				}
				handle(bytes, addr, mode)
			}
			for _, addr := range dnsAddrs {
				addr53 := addr + ":53"

				if tcpQuery {
					go h(addr53, "tcp", handleTCPDNS)
				}
				if udpQuery {
					go h(addr53, "udp", handleUDPDNS)
				}
			}
			if dohQuery {
				for _, addr := range dohHost {
					go h(addr, "doh", doDNSQuery)
				}
			}

		}
		if canUpdate {
			select {
			case response, ok := <-rChan:
				if ok {
					tRule.handleDns(response)
					if r == nil {
						_, _ = v.conn.WriteToUDP(response, v.oAddr)
					}

				}
			case <-time.After(5 * time.Second):
			}
		}

	})

	if !cach {
		os.RemoveAll(dnsDir())
	}

	lastTestTime := time.Now().Unix()
	for {
		// 读取来自原始发送方的消息
		n, origAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Debugln("[DNS] read err : %v", err)
			continue
		}
		if time.Now().Unix()-lastTestTime > int64(DnsCachTime) {
			go deleteDirSize(dnsDir(), 1024*1024*50)
			lastTestTime = time.Now().Unix()
		}
		msg := new(dns.Msg)
		err = msg.Unpack(buffer[:n])
		if err != nil {
			log.Debugln("[DNS] unpack err : %s", origAddr)
			continue
		}
		dnsBytes, _ := msg.Pack()
		golimiter.HandleValue(DNSV{oAddr: origAddr, conn: conn, bytes: dnsBytes})
	}
}

func handleDNSDirect(server, network string, bytes []byte) ([]byte, error) {

	m := new(dns.Msg)
	err := m.Unpack(bytes)

	if err != nil {
		return nil, fmt.Errorf("[DNS direct] dns unpack err: %v", err)
	}
	c := new(dns.Client)
	c.Net = network
	m.RecursionDesired = true

	r, _, err := c.Exchange(m, server+":53") // 注意端口53是DNS使用的标准UDP端口
	if err != nil {
		return nil, fmt.Errorf("[DNS direct] qurrey err: %v", err)
	}

	return r.Pack()
}

func handleTCPDNS(socks5Addr, dnsServerAddr string, dnsBytes []byte) ([]byte, error) {

	// 创建到 SOCKS5 代理的拨号器
	dialer, err := proxy.SOCKS5("tcp", socks5Addr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("[DNS TCP]Failed to create SOCKS5 dialer: %v", err)
	}

	// 使用拨号器创建连接
	conn, err := dialer.Dial("tcp", dnsServerAddr)
	if err != nil {
		return nil, fmt.Errorf("[DNS TCP]Failed to dial DNS server via SOCKS5: %v", err)
	}
	defer conn.Close()

	// 创建 DNS 客户端，并指定自定义的连接
	c := new(dns.Client)
	c.Net = "tcp" // 确保使用与 conn 匹配的网络类型

	// 构建 DNS 查询消息
	m := new(dns.Msg)
	err = m.Unpack(dnsBytes)
	if err != nil {
		return nil, fmt.Errorf("[DNS TCP]Failed to unpack DNS : %v", err)
	}
	// m.SetQuestion(dns.Fqdn("ipinfo.io"), dns.TypeAAAA)

	// 将 net.Conn 包装为 *dns.Conn
	conn.SetReadDeadline(time.Now().Add(time.Duration(DnsTimeout) * time.Second))
	dnsConn := &dns.Conn{Conn: conn}

	// 通过代理发送 DNS 查询
	r, _, err := c.ExchangeWithConn(m, dnsConn)
	if err != nil {
		return nil, fmt.Errorf("[DNS TCP] query failed: %v", err)
	}

	log.Debugln("[DNS TCP] Query result: %s", r.Answer)

	return r.Pack()

}

func handleUDPDNS(socks5Addr, dnsServerAddr string, dnsBytes []byte) ([]byte, error) {

	b, err := handleSocks5Udp(socks5Addr, dnsServerAddr, dnsBytes)

	if err != nil {
		return nil, fmt.Errorf("[DNS UDP]Failed to send dns : %v", err)
	}
	// 发送DNS查询
	r := new(dns.Msg)
	r.Unpack(b)

	log.Debugln("[DNS UDP] Query result: %s", r)

	return b, err

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
	buffer := make([]byte, 4096) // 大小应根据预期的数据包大小调整
	udpConn.SetReadDeadline(time.Now().Add(time.Duration(DnsTimeout) * time.Second))
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

func doDNSQuery(socks5Proxy, addr string, dnsBytes []byte) ([]byte, error) {

	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/dns-query", addr), bytes.NewReader(dnsBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}
	// 如果指定了SOCKS5代理
	// 创建HTTP客户端
	httpTransport := &http.Transport{}
	if socks5Proxy != "" {
		// 使用golang.org/x/net/proxy创建SOCKS5代理拨号器
		dialer, err := proxy.SOCKS5("tcp", socks5Proxy, nil, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %v", err)
		}

		// 设置http.Transport的DialContext函数
		httpTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	}
	req.Header.Set("Content-Type", "application/dns-message")

	client := &http.Client{Transport: httpTransport, Timeout: time.Duration(DnsTimeout) * time.Second}
	// 发送HTTP请求
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	// 读取并解析响应
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	return respBytes, nil
}

func RemoveDuplicates[T any, K comparable](slice []T, keyFunc func(T) K) []T {
	seen := make(map[K]bool)
	var result []T

	for _, item := range slice {
		key := keyFunc(item)
		if !seen[key] {
			seen[key] = true
			result = append(result, item)
		}
	}

	return result
}
