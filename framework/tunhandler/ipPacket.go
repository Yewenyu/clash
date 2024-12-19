package tunhandler

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"strings"

	"github.com/miekg/dns"
)

type IPPacket struct {
	Version       uint8
	HeaderLength  uint8
	TotalLength   uint16
	Protocol      uint8
	SourceIP      [4]byte
	DestinationIP [4]byte
	Payload       []byte
}

// Unpack parses a byte slice into an IPPacket
func Unpack(data []byte) (*IPPacket, error) {
	_, data, _ = unpackIPPacket(data)
	if len(data) < 20 {
		return nil, errors.New("data too short to be a valid IP packet")
	}

	packet := &IPPacket{}
	packet.Version = data[0] >> 4
	packet.HeaderLength = (data[0] & 0x0F) * 4

	if len(data) < int(packet.HeaderLength) {
		return nil, errors.New("data too short for specified header length")
	}

	packet.TotalLength = binary.BigEndian.Uint16(data[2:4])
	packet.Protocol = data[9]
	copy(packet.SourceIP[:], data[12:16])
	copy(packet.DestinationIP[:], data[16:20])

	if len(data) > int(packet.HeaderLength) {
		packet.Payload = data[packet.HeaderLength:]
	}

	return packet, nil
}

// Pack constructs a byte slice from an IPPacket
func (p *IPPacket) Pack() ([]byte, error) {
	if p.HeaderLength < 20 {
		return nil, errors.New("header length must be at least 20 bytes")
	}

	header := make([]byte, p.HeaderLength)
	header[0] = (p.Version << 4) | (p.HeaderLength / 4)
	binary.BigEndian.PutUint16(header[2:4], p.TotalLength)
	header[9] = p.Protocol
	copy(header[12:16], p.SourceIP[:])
	copy(header[16:20], p.DestinationIP[:])

	b := append(header, p.Payload...)

	return packIPPacket(b)
}

// 获取源IP的字符串表示
func (p *IPPacket) SourceIPString() string {
	return net.IP(p.SourceIP[:]).String()
}

// 获取目的IP的字符串表示
func (p *IPPacket) DestinationIPString() string {
	return net.IP(p.DestinationIP[:]).String()
}

const (
	AF_INET  = 2  // IPv4
	AF_INET6 = 10 // IPv6
)

func packIPPacket(ipPacket []byte) ([]byte, error) {
	if len(ipPacket) == 0 {
		return nil, errors.New("empty IP packet")
	}

	// 获取 IP version (前4位)
	version := (ipPacket[0] >> 4) & 0x0F

	// 根据版本决定 type
	var typeValue uint32
	if version == 4 {
		// IPv4
		typeValue = uint32(AF_INET)
	} else if version == 6 {
		// IPv6
		typeValue = uint32(AF_INET6)
	} else {
		return nil, errors.New("invalid IP version")
	}

	// 转为大端序
	var typeValueBytes [4]byte
	binary.BigEndian.PutUint32(typeValueBytes[:], typeValue)

	// 构造结果
	var result bytes.Buffer
	result.Write(typeValueBytes[:])
	result.Write(ipPacket)

	return result.Bytes(), nil
}

func unpackIPPacket(data []byte) (int64, []byte, error) {
	if len(data) < 4 {
		return 0, nil, errors.New("data too short")
	}

	// 读取 type (4 字节)
	typeValue := binary.BigEndian.Uint32(data[:4])

	// 提取 IP 数据包
	ipPacket := data[4:]

	return int64(typeValue), ipPacket, nil
}

// IsTCP checks if the packet is a TCP packet
func (p *IPPacket) IsTCP() bool {
	return p.Protocol == 6
}

// IsUDP checks if the packet is a UDP packet
func (p *IPPacket) IsUDP() bool {
	return p.Protocol == 17
}

// IsDNS checks if the packet is a DNS packet
// 修改点：不再只检查目的端口是53，也检查源端口是不是53。
// 这样无论请求还是应答，都能被识别为DNS包。
func (p *IPPacket) IsDNS() bool {
	if !(p.IsUDP() || p.IsTCP()) {
		return false
	}

	// 至少要有4字节来解析源端口与目的端口
	if len(p.Payload) < 4 {
		return false
	}

	// [0:2]为SourcePort, [2:4]为DestinationPort
	sourcePort := binary.BigEndian.Uint16(p.Payload[0:2])
	destPort := binary.BigEndian.Uint16(p.Payload[2:4])

	// 对于DNS，无论是请求(一般destPort=53)还是应答(一般sourcePort=53)
	// 满足其中一个即认为是DNS包
	if sourcePort == 53 || destPort == 53 {
		return true
	}
	return false
}

// GetDNSMessage returns the DNS message bytes
func (p *IPPacket) GetDNSMessage() ([]byte, error) {
	if !p.IsDNS() {
		return nil, errors.New("not a DNS packet")
	}

	if p.IsUDP() {
		// UDP首部固定8字节
		if len(p.Payload) < 8 {
			return nil, errors.New("invalid DNS packet payload (UDP)")
		}
		// DNS message starts after the UDP header (8 bytes)
		return p.Payload[8:], nil
	}

	if p.IsTCP() {
		// TCP首部长度不固定
		if len(p.Payload) < 20 {
			return nil, errors.New("invalid DNS packet payload (TCP too short)")
		}

		tcpHeaderLen := (p.Payload[12] >> 4) * 4
		if int(tcpHeaderLen) < 20 || int(tcpHeaderLen) > len(p.Payload) {
			return nil, errors.New("invalid TCP header length")
		}

		if len(p.Payload) < int(tcpHeaderLen)+2 {
			return nil, errors.New("invalid DNS packet payload (no length field)")
		}

		dnsLength := binary.BigEndian.Uint16(p.Payload[tcpHeaderLen : tcpHeaderLen+2])
		start := int(tcpHeaderLen) + 2
		end := start + int(dnsLength)

		if end > len(p.Payload) {
			return nil, errors.New("truncated DNS message in TCP payload")
		}

		dnsMessage := p.Payload[start:end]
		return dnsMessage, nil
	}

	return nil, errors.New("unknown transport protocol")
}

// GetDNSQueryNames 解析 DNS 查询包中的所有查询域名(QNAME)，以字符串切片返回。
// 假设该 IP 包是一个包含 DNS 查询的 UDP 包。
func (p *IPPacket) GetDNSQueryNames() ([]string, error) {
	dnsMessage, err := p.GetDNSMessage()
	if err != nil {
		return nil, err
	}

	// DNS 头部长度为12字节:
	// [0:2] ID, [2:4] Flags, [4:6] QDCount, [6:8] ANCount, [8:10] NSCount, [10:12] ARCount
	if len(dnsMessage) < 12 {
		return nil, errors.New("DNS message too short")
	}

	qdCount := binary.BigEndian.Uint16(dnsMessage[4:6])
	if qdCount == 0 {
		return nil, errors.New("no DNS questions in the message")
	}

	offset := 12
	var domainNames []string
	for i := 0; i < int(qdCount); i++ {
		domainName, nextOffset, err := parseQNAME(dnsMessage, offset)
		if err != nil {
			return nil, err
		}
		domainNames = append(domainNames, domainName)
		offset = nextOffset
	}

	return domainNames, nil
}

// parseQNAME 解析从 dnsMessage[offset] 开始的 QNAME，并返回解析出的域名字符串和下一个解析偏移量。
// QNAME 格式: sequence of labels ending in a zero-length label (0x00)
func parseQNAME(dnsMessage []byte, offset int) (string, int, error) {
	var labels []string
	for {
		if offset >= len(dnsMessage) {
			return "", 0, errors.New("truncated DNS QNAME")
		}
		length := int(dnsMessage[offset])
		if length == 0 {
			// QNAME 结束
			offset++
			break
		}
		offset++
		if offset+length > len(dnsMessage) {
			return "", 0, errors.New("truncated DNS label")
		}
		label := dnsMessage[offset : offset+length]
		labels = append(labels, string(label))
		offset += length
	}

	// QNAME解析完毕后，接下来是 QTYPE(2字节) 和 QCLASS(2字节)，共4字节
	if offset+4 > len(dnsMessage) {
		return "", 0, errors.New("truncated after QNAME, no QTYPE/QCLASS")
	}
	offset += 4 // 跳过QTYPE和QCLASS

	domainName := strings.Join(labels, ".")
	return domainName, offset, nil
}

// GetDNSServerAddress 返回 DNS 服务器 IP 地址（字符串格式）
// 对于发往 DNS 服务器的查询包，DNS 服务器通常为包的目的 IP。
func (p *IPPacket) GetDNSServerAddress() string {
	return net.IP(p.DestinationIP[:]).String()
}

func (p *IPPacket) toDNS() *dns.Msg {
	if !p.IsDNS() {
		return nil
	}
	b, _ := p.GetDNSMessage()
	msg := new(dns.Msg)
	_ = msg.Unpack(b)

	return msg
}
