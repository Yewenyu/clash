package tunhandler

import (
	"encoding/binary"
	"net"
	"strings"

	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/tunnel"
)

var (
	tRule *tunnel.TRule
)

func SetRule(r *tunnel.TRule) {
	tRule = r
}
func (p *IPPacket) SetDNSCach() {
	if p.IsDNS() && tRule != nil {
		dns, _ := p.GetDNSMessage()
		_ = tRule.HandleDns(dns)

	}
}
func (p *IPPacket) Match(proxyName string) bool {
	if tRule == nil || len(tRule.Rules) == 0 {
		return false
	}
	isContains := func(metadata *C.Metadata) bool {
		index, exist := tRule.Match(metadata)
		if exist {
			r := tRule.Rules[index]
			return strings.Contains(r.Adapter(), proxyName)
		}
		return false
	}
	if p.IsDNS() {
		metadata := p.ToMetadata()
		dnsHosts, err := p.GetDNSQueryNames()
		if err == nil {
			for _, h := range dnsHosts {
				metadata.Host = h
				contains := isContains(metadata)
				if contains {
					log.Debugln("[tun handle][rule match]dns %s query %s match [%s]", p.GetDNSServerAddress(), h, proxyName)
					return contains
				}
			}
		}
	}
	metadata := p.ToMetadata()

	return isContains(metadata)
}

func (p *IPPacket) ToMetadata() *C.Metadata {
	metadata := &C.Metadata{
		SrcIP: net.IP(p.SourceIP[:]),
		DstIP: net.IP(p.DestinationIP[:]),
	}

	if p.IsUDP() && len(p.Payload) >= 4 {
		metadata.SrcPort = C.Port(binary.BigEndian.Uint16(p.Payload[:2]))
		metadata.DstPort = C.Port(binary.BigEndian.Uint16(p.Payload[2:4]))
	}

	if p.IsDNS() {
		metadata.DNSMode = C.DNSNormal
	}

	return metadata
}
