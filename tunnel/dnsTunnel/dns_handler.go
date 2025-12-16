package dnstunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	gopool "github.com/Dreamacro/clash/goPool"
	"github.com/Dreamacro/clash/log"
	"github.com/miekg/dns"
	"golang.org/x/net/proxy"
)

func ListenDNS(localAddr, socks5Addr, mode string, cach bool, dnsAddrs []string, dohHost []string) {
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

	udpQuery, tcpQuery, dohQuery := false, false, false
	m := 0
	var udpDnsHandles []*UDPConnection

	if strings.Contains(mode, "udp") {
		udpQuery = true
		m++
		recvChan := make(chan *DNSInfo, 20)
		for _, addr := range dnsAddrs {
			udpDnsHandle, _ := NewUDPConnection(socks5Addr, addr+":53")
			udpDnsHandle.recvChan = recvChan
			udpDnsHandles = append(udpDnsHandles, udpDnsHandle)
		}

		go func() {
			for response := range recvChan {
				h, _ := response.handle.(func([]byte, error))
				if response.err != nil {
					msg := new(dns.Msg)
					if err := msg.Unpack(response.bytes); err != nil {
						response.err = fmt.Errorf("[DNS UDP] query error %v : %s", msg.Question, response.err)
					}
					response.bytes = nil
				}
				h(response.bytes, response.err)
			}
		}()
	}

	if strings.Contains(mode, "tcp") {
		tcpQuery = true
		m++
	}

	dnsCount := len(dnsAddrs) * m
	if strings.Contains(mode, "doh") {
		if len(dohHost) == 0 {
			dohHost = []string{"doh.opendns.com"}
		}
		dohQuery = true
		dnsCount += len(dohHost)
	}

	initTime := time.Now().Unix()
	dnsCanHandle := true
	var golimiter = gopool.NewGoroutinePool(MaxDnsConnectCount, func(v DNSV) {
		var l sync.Mutex
		rCount := 0
		rChan := make(chan []byte, dnsCount)

		handle := func(bytes []byte, dnsAddr, mode string) {
			l.Lock()
			rCount++
			l.Unlock()

			defer func() {
				if len(bytes) == 0 && rCount != dnsCount {
					return
				}
				rChan <- bytes
			}()

			if bytes == nil {
				return
			}

			msg := new(dns.Msg)
			if err := msg.Unpack(bytes); err != nil {
				return
			}

			if len(msg.Answer) == 0 {
				log.Debugln("[DNS response %s] empty answer %v from %s", mode, msg.Question, dnsAddr)
				return
			}

			log.Debugln("[DNS response %s] answer %s from %s", mode, msg.Answer, dnsAddr)
			if cach {
				Out_tRule.setDNSCach(bytes, &l)
			}
		}

		dnsBytes := v.bytes
		r, canUpdate := Out_tRule.getReponseDns(dnsBytes)
		if r != nil {
			if _, err := v.conn.WriteToUDP(r, v.oAddr); err != nil {
				log.Debugln("[DNS] write response error: %v", err)
			}
			l.Lock()
			dnsCanHandle = time.Now().Unix()-initTime < int64(DnsCachTime)
			l.Unlock()
			if dnsCanHandle {
				if err := Out_tRule.HandleDns(r); err != nil {
					log.Debugln("[DNS] handle dns error: %v", err)
				}
			}
		}

		if canUpdate {
			h := func(addr, mode string, f func(string, string, []byte) ([]byte, error)) {
				bytes, err := f(socks5Addr, addr, dnsBytes)
				if err != nil {
					log.Debugln("%v", err)
				}
				handle(bytes, addr, mode)
			}

			for index, addr := range dnsAddrs {
				addr53 := addr + ":53"
				if tcpQuery {
					go h(addr53, "tcp", handleTCPDNS)
				}
				if udpQuery {
					udpDnsHandle := udpDnsHandles[index]
					b := make([]byte, len(dnsBytes))
					copy(b, dnsBytes)
					udpDnsHandle.sendChan <- &DNSInfo{
						remoteAddr: addr53,
						bytes:      b,
						err:        nil,
						handle: func(bytes []byte, err error) {
							h(addr53, "udp", func(_, _ string, _ []byte) ([]byte, error) {
								return bytes, err
							})
						},
					}
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
			case response := <-rChan:
				if response != nil {
					if err := Out_tRule.HandleDns(response); err != nil {
						log.Debugln("[DNS] handle response error: %v", err)
					}
					if r == nil {
						if _, err := v.conn.WriteToUDP(response, v.oAddr); err != nil {
							log.Debugln("[DNS] write response error: %v", err)
						}
					}
				}
			case <-time.After(5 * time.Second):
			}
		}
	})

	os.RemoveAll(dnsDir())
	lastTestTime := time.Now().Unix()
	for {
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
		if err := msg.Unpack(buffer[:n]); err != nil {
			log.Debugln("[DNS] unpack err : %s", origAddr)
			continue
		}

		log.Debugln("[DNS Start] %v", msg.Question)
		dnsBytes, _ := msg.Pack()
		golimiter.SubmitTask(DNSV{oAddr: origAddr, conn: conn, bytes: dnsBytes})
	}
}

func handleDNSDirect(server, network string, bytes []byte) ([]byte, error) {
	m := new(dns.Msg)
	if err := m.Unpack(bytes); err != nil {
		return nil, fmt.Errorf("[DNS direct] dns unpack err: %v", err)
	}

	c := new(dns.Client)
	c.Net = network
	m.RecursionDesired = true

	r, _, err := c.Exchange(m, server)
	if err != nil {
		return nil, fmt.Errorf("[DNS direct] query err: %v", err)
	}

	return r.Pack()
}

func handleTCPDNS(socks5Addr, dnsServerAddr string, dnsBytes []byte) ([]byte, error) {

	if socks5Addr == "" {
		return handleDNSDirect(dnsServerAddr, "tcp", dnsBytes)
	}
	dialer, err := proxy.SOCKS5("tcp", socks5Addr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("[DNS TCP] create SOCKS5 dialer failed: %v", err)
	}

	conn, err := dialer.Dial("tcp", dnsServerAddr)
	if err != nil {
		return nil, fmt.Errorf("[DNS TCP] dial DNS server failed: %v", err)
	}
	defer conn.Close()

	c := new(dns.Client)
	c.Net = "tcp"
	conn.SetReadDeadline(time.Now().Add(time.Duration(DnsTimeout) * time.Second))
	dnsConn := &dns.Conn{Conn: conn}

	m := new(dns.Msg)
	if err := m.Unpack(dnsBytes); err != nil {
		return nil, fmt.Errorf("[DNS TCP] unpack DNS failed: %v", err)
	}

	r, _, err := c.ExchangeWithConn(m, dnsConn)
	if err != nil {
		return nil, fmt.Errorf("[DNS TCP] query failed: %v", err)
	}

	log.Debugln("[DNS TCP] Query result: %s", r.Answer)
	return r.Pack()
}

func doDNSQuery(socks5Proxy, addr string, dnsBytes []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/dns-query", addr), bytes.NewReader(dnsBytes))
	if err != nil {
		return nil, fmt.Errorf("create HTTP request failed: %v", err)
	}

	httpTransport := &http.Transport{}
	if socks5Proxy != "" {
		dialer, err := proxy.SOCKS5("tcp", socks5Proxy, nil, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create SOCKS5 dialer failed: %v", err)
		}
		httpTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	}

	req.Header.Set("Content-Type", "application/dns-message")
	client := &http.Client{
		Transport: httpTransport,
		Timeout:   time.Duration(DnsTimeout) * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body failed: %v", err)
	}

	return respBytes, nil
}
