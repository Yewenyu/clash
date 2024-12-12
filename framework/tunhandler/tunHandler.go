package tunhandler

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall"

	"github.com/Dreamacro/clash/log"
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

func readWrteFD(from, mtu int, flabel string, handle handleFdFunc) {
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
				log.Debugln("[tun handle][%s] Read failed: %v\n", flabel, err)
				break
			}
			if n > 0 {
				data := buffer[:n]
				to, tlabel := handle(data)
				log.Debugln("[tun handle][%s -> %s] read: %v\n", flabel, tlabel, n)

				// 将数据写入目标，处理部分写入的情况
				for len(data) > 0 {
					written, err := syscall.Write(to, data)
					if err != nil {
						log.Debugln("[tun handle][%s] Write failed: %v\n", tlabel, err)
						continue
					}
					data = data[written:] // 更新剩余未写入数据
					log.Debugln("[tun handle][%s -> %s] write: %v\n", flabel, tlabel, written)
				}
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
	if ruleProxy != "" {
		ruleProxys = append(ruleProxys, strings.Split(ruleProxy, ",")...)
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

	readWrteFD(tunFd, mtu, tunName, func(b []byte) (int, string) {

		// 不在这里先获取defaultKey，先尝试匹配
		p, err := Unpack(b)
		if err == nil && len(fdMap) > 1 {
			for k, v := range fdMap {
				// 跳过defaultKey，优先检查其他规则
				if k == defaultKey {
					continue
				}
				if p.Match(k) {
					return v.in, v.name
				}
			}
		}
		// 如果前面没匹配上，就fallback到defaultKey
		fdPipe := fdMap[defaultKey]
		return fdPipe.in, fdPipe.name
	})
	for _, v := range fdMap {
		readWrteFD(v.in, mtu, v.name, func(b []byte) (int, string) {
			p, err := Unpack(b)
			if err == nil {
				p.SetDNSCach()
			}
			return tunFd, tunName
		})
	}

	return outFD.toJsonString()
}
