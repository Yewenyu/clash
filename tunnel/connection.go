package tunnel

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	connmanager "github.com/Dreamacro/clash/common/connManager"
	N "github.com/Dreamacro/clash/common/net"
	"github.com/Dreamacro/clash/common/pool"
	C "github.com/Dreamacro/clash/constant"
	gopool "github.com/Dreamacro/clash/goPool"
	dnstunnel "github.com/Dreamacro/clash/tunnel/dnsTunnel"
	"github.com/alitto/pond/v2"
)

func handleUDPToRemote(packet C.UDPPacket, pc C.PacketConn, metadata *C.Metadata) error {
	addr := metadata.UDPAddr()
	if addr == nil {
		return errors.New("udp addr invalid")
	}
	bytes := packet.Data()
	// if strings.Contains(addr.String(), "53") {
	// 	msg := new(dns.Msg)
	// 	if err := msg.Unpack(bytes); err != nil {
	// 		log.Debugln("[DNS UDP] query error %v : %s", msg.Question, err)

	// 	} else {
	// 		if strings.Contains(msg.Question[0].Name, "bilivideo.com") {
	// 			_ = msg
	// 			bytes, _ = msg.Pack()
	// 		}
	// 	}

	// }

	if _, err := pc.WriteTo(bytes, addr); err != nil {
		return err
	}
	// reset timeout
	pc.SetReadDeadline(time.Now().Add(time.Duration(N.UdpTimeOut) * time.Second))

	return nil
}

var udpLock sync.Mutex
var udpQueuePool *N.QueuePool

type udpInfo struct {
	packet       C.UDPPacket
	pc           net.PacketConn
	oAddr, fAddr netip.Addr
	key          string
	activeTime   time.Time
	timeout      int
	lock         sync.Mutex
	stop         bool
}

func (u *udpInfo) Key() string {
	return u.key
}

func (u *udpInfo) SetActiveTime(time time.Time) {
	u.activeTime = time
}
func (u *udpInfo) IsDns() bool {
	return false
}
func (u *udpInfo) Lock() *sync.Mutex {
	return &u.lock
}

func (u *udpInfo) ActiveTime() time.Time {
	return u.activeTime
}

func (u *udpInfo) IsStop() bool {
	return u.stop
}

func (u *udpInfo) Close() {
	natTable.Delete(u.key)
	u.pc.Close()
	u.stop = true
}
func (u *udpInfo) Timeout() int {
	return u.timeout
}
func (u *udpInfo) SetTimeout(timeout int) {
	u.timeout = timeout
}
func (u *udpInfo) GetGoPool() pond.Pool {
	return gopool.SubGo
}

func (u *udpInfo) Relay() {
	pc := u.pc
	oAddr := u.oAddr
	fAddr := u.fAddr
	packet := u.packet
	buf := make([]byte, pool.UDPBufferSize)
	defer u.Close()

	for {
		u.lock.Lock()
		pc.SetReadDeadline(time.Now().Add(time.Duration(u.timeout) * time.Second))
		u.lock.Unlock()
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		fromUDPAddr := *from.(*net.UDPAddr)
		if fAddr.IsValid() {
			fromAddr, _ := netip.AddrFromSlice(fromUDPAddr.IP)
			fromAddr = fromAddr.Unmap()
			if oAddr == fromAddr {
				fromUDPAddr.IP = fAddr.AsSlice()
			}
		}
		if strings.Contains(from.String(), "53") && !dnstunnel.Out_tRule.IsHttpEnable {
			bytes := buf[:n]
			dnstunnel.Out_tRule.HandleDnsWithChan(bytes)

		}

		_, err = packet.WriteBack(buf[:n], &fromUDPAddr)
		if err != nil {
			return
		}
	}
}

func handleUDPToLocal(packet C.UDPPacket, pc net.PacketConn, key string, oAddr, fAddr netip.Addr) {
	udpLock.Lock()
	if udpQueuePool == nil {
		udpQueuePool = N.NewQueuePool(connmanager.TCPMaxCount)
	}
	udpLock.Unlock()
	info := udpInfo{
		packet:     packet,
		pc:         pc,
		key:        key,
		oAddr:      oAddr,
		fAddr:      fAddr,
		activeTime: time.Now(),
		timeout:    N.UdpTimeOut,
	}
	udpQueuePool.AddConns(&info)

}

func handleSocket(ctx C.ConnContext, outbound net.Conn, useHttpTimeout, useDNSTimeout bool) {
	N.Relay(ctx.Conn(), outbound, useHttpTimeout, useDNSTimeout)
}
