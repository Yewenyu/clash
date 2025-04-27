package dnstunnel

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/transport/socks5"
	"github.com/miekg/dns"
)

type DNSInfo struct {
	remoteAddr string
	bytes      []byte
	err        error
	handle     interface{}
}

type UDPConnection struct {
	conn                  *net.UDPConn
	sendChan              chan *DNSInfo
	recvChan              chan *DNSInfo
	queryMap              map[uint16]*DNSInfo
	mapMutex              sync.Mutex
	wg                    sync.WaitGroup
	proxyAddr, remoteAddr string
	connectMutex          sync.Mutex
}

func NewUDPConnection(proxyAddr string, remoteAddr string) (*UDPConnection, error) {
	uc := &UDPConnection{
		proxyAddr:  proxyAddr,
		remoteAddr: remoteAddr,
		sendChan:   make(chan *DNSInfo, 20),
		recvChan:   make(chan *DNSInfo),
		queryMap:   make(map[uint16]*DNSInfo),
	}
	uc.wg.Add(2)
	go uc.sender()
	go uc.receiver()
	return uc, nil
}

func (uc *UDPConnection) initConnect() {
	uc.connectMutex.Lock()
	defer uc.connectMutex.Unlock()

	if uc.conn != nil {
		return
	}

	tcpConn, err := net.Dial("tcp", uc.proxyAddr)
	if err != nil {
		log.Errorln("dial TCP failed: %v", err)
		return
	}
	defer tcpConn.Close()

	addr, err := socks5.ClientHandshake(tcpConn, socks5.ParseAddr(uc.remoteAddr), socks5.CmdUDPAssociate, nil)
	if err != nil {
		log.Errorln("SOCKS5 handshake failed: %v", err)
		return
	}

	boundUDPAddr := addr.UDPAddr()
	udpConn, err := net.DialUDP("udp", nil, boundUDPAddr)
	if err != nil {
		log.Errorln("dial UDP failed: %v", err)
		return
	}
	uc.conn = udpConn
}

func (uc *UDPConnection) sender() {
	defer uc.wg.Done()
	for info := range uc.sendChan {
		for uc.conn == nil {
			uc.initConnect()
		}

		msg := new(dns.Msg)
		if err := msg.Unpack(info.bytes); err != nil {
			info.err = err
			uc.recvChan <- info
			continue
		}

		packet, err := socks5.EncodeUDPPacket(socks5.ParseAddr(info.remoteAddr), info.bytes)
		if err != nil {
			info.err = err
			uc.recvChan <- info
			continue
		}

		uc.mapMutex.Lock()
		uc.queryMap[msg.Id] = info
		uc.mapMutex.Unlock()

		go func(id uint16) {
			<-time.After(5 * time.Second)
			uc.mapMutex.Lock()
			defer uc.mapMutex.Unlock()
			if info, found := uc.queryMap[id]; found {
				delete(uc.queryMap, id)
				info.err = fmt.Errorf("timeout")
				uc.recvChan <- info
			}
		}(msg.Id)

		if _, err := uc.conn.Write(packet); err != nil {
			info.err = err
			uc.recvChan <- info
		}
	}
}

func (uc *UDPConnection) receiver() {
	defer uc.wg.Done()
	buffer := make([]byte, 4096)
	for {
		if uc.conn == nil {
			time.Sleep(time.Second)
			continue
		}

		n, _, err := uc.conn.ReadFromUDP(buffer)
		if err != nil {
			continue
		}

		_, payload, err := socks5.DecodeUDPPacket(buffer[:n])
		if err != nil {
			continue
		}

		response := new(dns.Msg)
		if err := response.Unpack(payload); err != nil {
			continue
		}

		uc.mapMutex.Lock()
		query, found := uc.queryMap[response.Id]
		if found {
			delete(uc.queryMap, response.Id)
		}
		uc.mapMutex.Unlock()

		if found {
			query.bytes = payload
			uc.recvChan <- query
		}
	}
}

func (uc *UDPConnection) Close() {
	close(uc.sendChan)
	for len(uc.recvChan) > 0 {
		time.Sleep(100 * time.Millisecond)
	}
	if uc.conn != nil {
		uc.conn.Close()
	}
	uc.wg.Wait()
}
