package tunhandler

import (
	"net"
	"runtime"
	"sync"

	"github.com/Dreamacro/clash/log"
	"github.com/miekg/dns"
)

var (
	captureChan = make(chan []byte, 10)
	onceHandle  sync.Once

	CanCapture = false
)

// StartCapture 启动ip数据包捕获
func StartCapture(b []byte) {
	if CanCapture {
		newB := append([]byte{}, b...)
		go PacketCapture(newB)
	}
}

func PacketCapture(bytes []byte) {

	onceHandle.Do(func() {
		// err := loadHostInfoFromFile(WritePath)
		// if err != nil {
		// 	log.Debugln("[Packet Capture] err:%v", err)
		// }
		handle := func() {
			for {
				b := <-captureChan

				b = append([]byte{}, b...)
				ipPacket, err := Unpack(b)
				if err != nil {
					log.Debugln("[Packet Capture]Unpack err:%v", err)
					continue
				}
				go func(ipPacket *IPPacket) {
					if ipPacket.IsDNS() {
						msg := ipPacket.toDNS() // 假设已定义的函数
						log.Debugln("[Packet Capture] msg:%s", msg.String())
						if len(msg.Answer) > 0 {
							host := trimLastDot(msg.Question[0].Name)
							isIp := isIPAddress(host)
							for _, rr := range msg.Answer {
								var ip = ""
								switch v := rr.(type) {
								case *dns.A:
									ip = v.A.String()
								case *dns.AAAA:
									ip = v.AAAA.String()
								}
								h := trimLastDot(rr.Header().Name)
								//判断host是否ip
								if !isIp {
									h = host
								}
								HandleHostInfo(h, ip, "dns查询")

							}
							if isIp {
								HandleHostInfo(host, "", "dns查询")
							}
						}
					} else {
						dest := ipPacket.DestinationIPString()
						src := ipPacket.SourceIPString()
						HandleHostInfo("", dest, ipPacket.ProtocolString())
						HandleHostInfo("", src, ipPacket.ProtocolString())
						log.Debugln("[Packet Capture] dest:%s, src:%s", dest, src)

					}
					runtime.GC()
				}(ipPacket)

			}
		}
		go handle()
	})

	captureChan <- bytes

}

func isIPAddress(host string) bool {
	return net.ParseIP(host) != nil
}

func trimLastDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}
