package tunhandler

import (
	"bytes"
	"encoding/binary"
	"errors"
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
func (p *IPPacket) IsDNS() bool {
	if !p.IsUDP() {
		return false
	}
	if len(p.Payload) < 2 {
		return false
	}
	// DNS typically uses port 53
	port := binary.BigEndian.Uint16(p.Payload[:2])
	return port == 53
}

func (p *IPPacket) GetDNSMessage() ([]byte, error) {
	if !p.IsDNS() {
		return nil, errors.New("not a DNS packet")
	}
	if len(p.Payload) < 8 {
		return nil, errors.New("invalid DNS packet payload")
	}
	// DNS message starts after the UDP header (8 bytes)
	return p.Payload[8:], nil
}
