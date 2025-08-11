package tunhandler

import (
	"encoding/json"
	"fmt"
	"gts"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/tunnel"
	dnstunnel "github.com/Dreamacro/clash/tunnel/dnsTunnel"
)

func setSocketBufferSize(fd int, size int) error {
	// 设置接收缓冲区大小
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, size); err != nil {
		return fmt.Errorf("failed to set SO_RCVBUF: %v", err)
	}

	// 设置发送缓冲区大小
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, size); err != nil {
		return fmt.Errorf("failed to set SO_SNDBUF: %v", err)
	}

	return nil
}
func setNonBlocking(fd int) error {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFL), 0)
	if errno != 0 {
		return fmt.Errorf("fcntl get failed: %v", errno)
	}

	_, _, errno = syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_SETFL), flags|syscall.O_NONBLOCK)
	if errno != 0 {
		return fmt.Errorf("fcntl set failed: %v", errno)
	}
	return nil
}
func createPipe() (int, int, error) {
	// 创建 socketpair
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0)
	if err != nil {
		log.Debugln("Socketpair creation failed: %v\n", err)
		return -1, -1, err
	}

	fd1 := fds[0]
	fd2 := fds[1]

	// 设置 fd1 和 fd2 为非阻塞模式
	if err := setNonBlocking(fd1); err != nil {
		log.Debugln("Failed to set fd1 non-blocking: %v\n", err)
		return -1, -1, err
	}
	if err := setNonBlocking(fd2); err != nil {
		log.Debugln("Failed to set fd2 non-blocking: %v\n", err)
		return -1, -1, err
	}
	setSocketBufferSize(fd1, 1024*1024)
	setSocketBufferSize(fd2, 1024*1024)

	return fd1, fd2, nil
}

type handleFdFunc = func([]byte) (int, string)
type WriteFunc = func([]byte)

func writeFD(fd int, bytes []byte) {
	// 将数据写入目标，处理部分写入的情况
	for len(bytes) > 0 {
		written, err := syscall.Write(fd, bytes)
		if err != nil {
			// log.Debugln("[tun handle][%s] Write failed: %v\n", tlabel, err)
			continue
		}
		bytes = bytes[written:] // 更新剩余未写入数据
	}
}
func readWrteFD(from, mtu int, flabel string, handle handleFdFunc, writeFunc WriteFunc) {
	go func() {
		buffer := make([]byte, mtu)
		for {
			// 读取数据
			n, err := syscall.Read(from, buffer)
			if err != nil {
				if err == syscall.EAGAIN {
					// 非阻塞模式下没有数据可读时，跳过并继续
					continue
				}
				// log.Debugln("[tun handle][%s] Read failed: %v\n", flabel, err)
				break
			}
			if n > 0 {
				data := buffer[:n]
				if writeFunc != nil {
					writeFunc(data)
					continue
				}
				to, tlabel := handle(data)
				_ = tlabel
				// log.Debugln("[tun handle][%s -> %s] read: %v\n", flabel, tlabel, n)

				writeFD(to, data)
			}
		}
	}()
}

type fdPipe struct {
	in, out int
	name    string
}

type OutFD struct {
	DefaultFd int            `json:"default_fd"`
	ProxyFD   map[string]int `json:"proxy_fd"`
}

func (out OutFD) toJsonString() string {
	// 将结构体转换为 JSON 字符串
	jsonData, err := json.Marshal(out)
	if err != nil {
		fmt.Println("Error converting to JSON:", err)
		return ""
	}
	return string(jsonData)
}

func CreateFD(tunFd int, mtu int, ruleProxy string) string {
	defaultKey := "default"

	ruleProxys := []string{defaultKey}

	var handleProxy = false
	starTun := func(logS string) {

	}
	defer func() {
		if !handleProxy {
			go starTun("default tun")
		}
	}()

	if ruleProxy != "" {
		handleProxy = true
		proxys := strings.Split(ruleProxy, ",")
		tunnel.SetHandleRule(func(t *dnstunnel.TRule) *dnstunnel.TRule {
			rs := t.Rules
			directRules := make([]constant.Rule, 0)
			proxyRules := make([]constant.Rule, 0)
			proxyS := ""
			directS := ""
			for _, r := range rs {
				if slices.Contains(proxys, r.Adapter()) {
					proxyRules = append(proxyRules, r)
					proxyS += r.Adapter() + "-"
				} else {
					directRules = append(directRules, r)
					directS += r.Adapter() + "-"

				}
				if r.Adapter() == "DIRECT" {
					proxyRules = append(proxyRules, r)
					directRules = append(directRules, r)
					proxyS += r.Adapter() + "-"
					directS += r.Adapter() + "-"
				}
			}
			SetRule(dnstunnel.CreateTRule(proxyRules))
			t.Rules = directRules
			go starTun(fmt.Sprintf("tun proxys:%s,proxyRules:%s,directRules:%s", proxys, proxyS, directS))
			return t
		})
		ruleProxys = append(ruleProxys, proxys...)
	}
	fdMap := make(map[string]fdPipe)
	outFD := OutFD{ProxyFD: make(map[string]int)}
	for _, r := range ruleProxys {
		fd1, fd2, err := createPipe()
		if err != nil {
			log.Debugln("Socketpair creation failed: %v\n", err)
			return ""
		}

		fdMap[r] = fdPipe{in: fd1, out: fd2, name: r}
		if r == defaultKey {
			outFD.DefaultFd = fd2
		} else {
			outFD.ProxyFD[r] = fd2
		}
	}
	tunName := "tunFd"

	starTun = func(logS string) {
		log.Infoln("startTun handle proxy: %v", logS)
		readWrteFD(tunFd, mtu, tunName, func(b []byte) (int, string) {

			StartCapture(b)
			// 不在这里先获取defaultKey，先尝试匹配
			p, err := Unpack(b)
			if err == nil && len(fdMap) > 1 {
				for k, v := range fdMap {
					// 跳过defaultKey，优先检查其他规则
					if k == defaultKey {
						continue
					}
					if p.Match(k) {
						log.Debugln("[tun handle][rule match]%s match [%s]", p.DestinationIPString(), k)
						return v.in, v.name
					}
				}
			}

			// 如果前面没匹配上，就fallback到defaultKey
			fdPipe := fdMap[defaultKey]
			log.Debugln("[tun handle][rule match]%s match [%s]", p.DestinationIPString(), defaultKey)
			return fdPipe.in, fdPipe.name
		}, nil)

		bytesChan := make(chan []byte, len(fdMap)*10)
		go func() {
			for {
				b := <-bytesChan
				StartCapture(b)
				writeFD(tunFd, b)
			}
		}()
		for _, v := range fdMap {
			readWrteFD(v.in, mtu, v.name, nil, func(b []byte) {
				p, err := Unpack(b)
				if err == nil {
					go p.SetDNSCach()
				}
				newb := append([]byte(nil), b...)
				go func(b []byte) { bytesChan <- b }(newb)
			})
		}
	}

	return outFD.toJsonString()
}

type TunTestConfig struct {
	RuleProxy string `json:"rule_proxy"`
	GtsConfig string `json:"gts_config"`
}
type TunBytes struct {
	TunKey string `json:"tun_key"`
	Bytes  []byte `json:"bytes"`
}
type BytesLength struct {
	Length int `json:"length"`
}

var (
	directKey = "default"
	outKey    = "Out"
)

func StartListenFD(port int) {
	log.Infoln("StartListenFD port: %d", port)

	// 从命令行参数获取端口（如果提供）
	if len(os.Args) > 1 {
		p, err := strconv.Atoi(os.Args[1])
		if err == nil && p > 0 && p <= 65535 {
			port = p
		} else {
			log.Fatalln("无效的端口号，使用默认端口 8080")
		}
	}

	// 创建UDP地址结构
	addr := net.UDPAddr{
		IP:   net.IPv4(0, 0, 0, 0), // 监听所有可用网络接口
		Port: port,
	}

	// 监听UDP端口
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		log.Fatalln("无法监听UDP端口 %d: %v", port, err)
	}
	defer conn.Close()

	log.Infoln("UDP服务器已启动，正在监听端口 %d...", port)

	// 缓冲区用于接收数据
	buffer := make([]byte, 4096)

	fd1, fd2 := createTestFD()
	if fd1 == -1 || fd2 == -1 {
		log.Fatalln("CreateTestFD failed")
	}

	// 持续接收数据
	bytes := make([]byte, 0)
	var bLength *BytesLength
	outFd := -1

	var once sync.Once

	for {
		// 读取UDP数据包
		n, clientAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Debugln("接收数据错误: %v", err)
			continue
		}
		once.Do(func() {
			readWrteFD(fd1, 1500, "udp", nil, func(b []byte) {
				tunBytes := TunBytes{
					TunKey: directKey,
					Bytes:  b,
				}
				bytes, _ := json.Marshal(tunBytes)
				writeToUdp(conn, bytes, clientAddr)
			})
		})
		if bLength == nil {
			bLength = &BytesLength{}
			err = json.Unmarshal(buffer[:n], bLength)
			if err != nil {
				log.Debugln("Unmarshal error: %v", err)
				continue
			}
		}
		bytes = append(bytes, buffer[:n]...)
		if len(bytes) < bLength.Length {
			continue
		}

		bLength = nil
		var tunBytes TunBytes
		err = json.Unmarshal(bytes, &tunBytes)
		if err == nil {
			if tunBytes.TunKey == directKey {
				writeFD(fd2, tunBytes.Bytes)
			} else {
				writeFD(outFd, tunBytes.Bytes)
			}
			continue
		}
		var tunTest TunTestConfig
		err = json.Unmarshal(bytes, &tunTest)
		if err == nil {
			log.Infoln("tunTest: %v", tunTest)
			outFd = handleTunTest(tunTest, fd2)
			go readWrteFD(outFd, 1500, "udp", nil, func(b []byte) {
				tunBytes := TunBytes{
					TunKey: outKey,
					Bytes:  b,
				}
				bytes, _ := json.Marshal(tunBytes)
				writeToUdp(conn, bytes, clientAddr)
			})
			continue
		}

	}
}
func writeToUdp(conn *net.UDPConn, b []byte, addr *net.UDPAddr) {
	bLength := &BytesLength{
		Length: len(b),
	}
	blBytes, _ := json.Marshal(bLength)
	conn.WriteToUDP(blBytes, addr)
	conn.WriteToUDP(b, addr)
}

func StartTest(fd int, addr string, gtsConfig string) int {

	spilt := strings.Split(addr, ":")
	port, _ := strconv.Atoi(spilt[2])
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.IP(spilt[1]),
		Port: port,
	})
	if err != nil {
		log.Debugln("DialUDP error: %v", err)
		return -1
	}
	fd1, fd2 := createTestFD()

	go readWrteFD(fd, 1500, "udp", nil, func(b []byte) {
		tunBytes := TunBytes{
			TunKey: directKey,
			Bytes:  b,
		}
		bytes, _ := json.Marshal(tunBytes)
		writeToUdp(conn, bytes, nil)
	})
	go readWrteFD(fd1, 1500, "udp", nil, func(b []byte) {
		tunBytes := TunBytes{
			TunKey: outKey,
			Bytes:  b,
		}
		bytes, _ := json.Marshal(tunBytes)
		writeToUdp(conn, bytes, nil)
	})

	go func() {
		b := make([]byte, 1600)
		var bLength *BytesLength
		bytes := make([]byte, 0)
		for {

			n, _, err := conn.ReadFromUDP(b)
			if err != nil {
				log.Debugln("ReadFromUDP error: %v", err)
				continue
			}

			if bLength == nil {
				bLength = &BytesLength{}
				err = json.Unmarshal(b[:n], &bLength)
				if err != nil {
					log.Debugln("Unmarshal error: %v", err)
					bLength = nil
				}
				continue
			}
			if len(b) < bLength.Length {
				continue
			}
			bytes = append(bytes, b[:n]...)
			if len(bytes) < bLength.Length {
				continue
			}
			var tunBytes TunBytes
			err = json.Unmarshal(bytes, &tunBytes)
			if err == nil {
				if tunBytes.TunKey == directKey {
					writeFD(fd, tunBytes.Bytes)
				} else {
					writeFD(fd1, tunBytes.Bytes)
				}
			}
		}
	}()
	return fd2
}
func handleTunTest(tunTest TunTestConfig, fd2 int) int {
	ruleProxy := tunTest.RuleProxy
	s := CreateFD(fd2, 1500, ruleProxy)
	var outFd OutFD
	err := json.Unmarshal([]byte(s), &outFd)
	if err != nil {
		log.Debugln("Unmarshal error: %v", err)
		return -1
	}
	fd := outFd.DefaultFd
	v, success := outFd.ProxyFD[ruleProxy]
	out := -1
	if success {
		out = fd
		fd = v
	}
	go gts.StartGTSWith(tunTest.GtsConfig, fd)
	return out
}

func createTestFD() (int, int) {
	fd1, fd2, err := createPipe()
	if err != nil {
		log.Debugln("Socketpair creation failed: %v\n", err)
		return -1, -1
	}
	return fd1, fd2
}
