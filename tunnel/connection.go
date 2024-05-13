package tunnel

import (
	"errors"
	"net"
	"net/netip"
	"time"

	N "github.com/Dreamacro/clash/common/net"
	"github.com/Dreamacro/clash/common/pool"
	C "github.com/Dreamacro/clash/constant"
)

func handleUDPToRemote(packet C.UDPPacket, pc C.PacketConn, metadata *C.Metadata) error {
	addr := metadata.UDPAddr()
	if addr == nil {
		return errors.New("udp addr invalid")
	}
	bytes := packet.Data()

	if _, err := pc.WriteTo(bytes, addr); err != nil {
		return err
	}
	// reset timeout
	pc.SetReadDeadline(time.Now().Add(time.Duration(N.UdpTimeOut) * time.Second))

	return nil
}

func handleUDPToLocal(packet C.UDPPacket, pc net.PacketConn, key string, oAddr, fAddr netip.Addr) {
	buf := pool.Get(pool.UDPBufferSize)
	defer pool.Put(buf)
	defer natTable.Delete(key)
	defer pc.Close()

	for {
		pc.SetReadDeadline(time.Now().Add(time.Duration(N.UdpTimeOut) * time.Second))
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

		_, err = packet.WriteBack(buf[:n], &fromUDPAddr)
		if err != nil {
			return
		}
	}
}

func handleSocket(ctx C.ConnContext, outbound net.Conn) {
	N.Relay(ctx.Conn(), outbound)
}
