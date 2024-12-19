package tunhandler

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"syscall"

	"github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/tunnel"
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
		tunnel.SetHandleRule(func(t *tunnel.TRule) *tunnel.TRule {
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
			SetRule(tunnel.CreateTRule(proxyRules))
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
