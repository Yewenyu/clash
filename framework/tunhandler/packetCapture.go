package tunhandler

import (
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
		go func() {
			for {
				b := <-captureChan
				b = append([]byte{}, b...)
				ipPacket, err := Unpack(b)
				if err != nil {
					log.Debugln("[Packet Capture]Unpack err:%v", err)
					continue
				}
				if ipPacket.IsDNS() {
					msg := ipPacket.toDNS() // 假设已定义的函数
					log.Debugln("[Packet Capture] msg:%s", msg.String())
					if len(msg.Answer) > 0 {
						host := trimLastDot(msg.Question[0].Name)
						for _, rr := range msg.Answer {
							var ip = ""
							switch v := rr.(type) {
							case *dns.A:
								ip = v.A.String()
							case *dns.AAAA:
								ip = v.AAAA.String()
							}
							HandleHostInfo(host, ip)

						}
					}
				} else {
					dest := ipPacket.DestinationIPString()
					src := ipPacket.SourceIPString()
					HandleHostInfo("", dest)
					HandleHostInfo("", src)
					log.Debugln("[Packet Capture] dest:%s, src:%s", dest, src)

				}
			}
		}()
	})

	captureChan <- bytes

}

func trimLastDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}
