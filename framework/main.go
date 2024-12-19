package clash

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
*/
import "C"

import (
	"fmt"
	"gts"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"
	"tunhandler"

	"github.com/Dreamacro/clash/adapter/provider"
	"github.com/Dreamacro/clash/config"
	"github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/hub/executor"
	"github.com/Dreamacro/clash/log"
	t "github.com/Dreamacro/clash/tunnel"
	"github.com/Dreamacro/clash/tunnel/statistic"

	// "github.com/eycorsican/go-tun2socks/client"

	"net/http"
	_ "net/http/pprof"

	connmanager "github.com/Dreamacro/clash/common/connManager"
	N "github.com/Dreamacro/clash/common/net"
	"github.com/Dreamacro/clash/common/pool"
)

// framework support

func ReadConfig(path string) ([]byte, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("Configuration file %s is empty", path)
	}
	return data, err
}

func GetRawCfgByPath(path string) (*config.RawConfig, error) {
	if len(path) == 0 {
		path = constant.Path.Config()
	}

	buf, err := ReadConfig(path)
	if err != nil {
		return nil, err
	}
	return config.UnmarshalRawConfig(buf)
}

func SetupHomeDir(homeDirPath string) {
	constant.SetHomeDir(homeDirPath)
}

var cfgPath = ""
var externalControllerAddr = ""

func RunByConfig(configString string, externalController string) error {
	log.Infoln("start run")
	// cfgPath = configPath
	externalControllerAddr = externalController
	// constant.SetConfig(configPath)
	rawConfig, err := config.UnmarshalRawConfig([]byte(configString))
	if err != nil {
		return err
	}
	log.Infoln("current rawconfig mixedPort: %d", rawConfig.MixedPort)
	log.Infoln("current rawconfig mode: %d", rawConfig.Mode)
	rawConfig.ExternalUI = ""
	rawConfig.Profile.StoreSelected = false
	if len(externalController) > 0 {
		rawConfig.ExternalController = externalController
	}

	cfg, err := config.ParseRawConfig(rawConfig)
	if err != nil {
		log.Infoln("config.parse raw config failed by error: %s", err.Error())
		return err
	}
	// go route.Start(externalController, "")
	executor.ApplyConfig(cfg, true)
	log.Infoln("apply config success")
	return nil

}

func CloseAllConnections() {
	snapshot := statistic.DefaultManager.Snapshot()
	for _, c := range snapshot.Connections {
		c.Close()
	}
}

/*
*

	PanicLevel Level = iota 0

	// FatalLevel level. Logs and then calls `logger.Exit(1)`. It will exit even if the
	// logging level is set to Panic.
	FatalLevel 1
	// ErrorLevel level. Logs. Used for errors that should definitely be noted.
	// Commonly used for hooks to send errors to an error tracking service.
	ErrorLevel 2
	// WarnLevel level. Non-critical entries that deserve eyes.
	WarnLevel 3
	// InfoLevel level. General operational entries about what's going on inside the
	// application.
	InfoLevel 4
	// DebugLevel level. Usually only enabled when debugging. Very verbose logging.
	DebugLevel 5
	// TraceLevel level. Designates finer-grained informational events than the Debug.
	TraceLevel 6

*
*/
func CustomLogFile(logPath string, level int, maxCount int) {
	log.CustomLogPath(logPath, level, maxCount)
}

func SetGCPrecent(v int) {
	debug.SetGCPercent(v)
}
func FreeOSMemory() {
	debug.FreeOSMemory()
}
func SetBufferSize(tcp, udp int) {
	N.TCPBufferSize = tcp
	pool.RelayBufferSize = tcp
	pool.UDPBufferSize = udp
}

func SetMixMaxCount(mix, tcp, udp int) {
	connmanager.MixedMaxCount = mix
	connmanager.TCPMaxCount = tcp
	t.ProcessUDP(udp)
}
func DNSCachTime(second int) {
	t.DnsCachTime = second
}
func SetConnTimeout(tcp, udp int) {
	N.TcpTimeout = tcp
	N.UdpTimeOut = udp
}

type InfoCallBack interface {
	HealthTest(result string)
}

func SetCallBack(callBack InfoCallBack, urlTestTimeoutSecond int) {
	provider.URLTestTimeout = urlTestTimeoutSecond
	provider.HealthCheckCallBack = func(result string) {
		callBack.HealthTest(result)
	}
}

func ListenDNS(localAddr, socks5Addr, mode string, cach bool, dnsAddrs, dohHosts string, maxDnsConnectCount int) {
	t.MaxDnsConnectCount = maxDnsConnectCount
	go t.ListenDNS(localAddr, socks5Addr, mode, cach, strings.Split(dnsAddrs, ","), strings.Split(dohHosts, ","))
}

func ListenUDP(targetAddr, localAddr string) {
	// 服务器监听的地址
	serverAddr := localAddr
	// 第三方服务的地址
	thirdPartyAddr := targetAddr
	serverUDPAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		fmt.Println("解析服务器地址出错：", err)
		os.Exit(1)
	}

	thirdPartyUDPAddr, err := net.ResolveUDPAddr("udp", thirdPartyAddr)
	if err != nil {
		fmt.Println("解析第三方服务地址出错：", err)
		os.Exit(1)
	}

	conn, err := net.ListenUDP("udp", serverUDPAddr)
	if err != nil {
		fmt.Println("服务器监听出错：", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Printf("服务器正在监听 %s\n", serverAddr)
	fmt.Printf("准备将消息转发到 %s\n", thirdPartyAddr)

	buffer := make([]byte, 1024)

	for {
		// 读取来自原始发送方的消息
		n, origAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			fmt.Println("读取错误：", err)
			continue
		}
		message := make([]byte, n)
		copy(message, buffer[:n])
		// 将消息转发到第三方服务
		forwardConn, err := net.DialUDP("udp", nil, thirdPartyUDPAddr)
		go func(message []byte, conn *net.UDPConn) {
			if err != nil {
				fmt.Printf("连接到第三方服务失败： %v\n", err)
				return
			}
			_, err = conn.Write(message)
			if err != nil {
				fmt.Println("转发到第三方服务失败：", err)
				return
			}
		}(message, forwardConn)

		go func(forwardConn *net.UDPConn, conn *net.UDPConn, oAddr *net.UDPAddr) {
			replyBuffer := make([]byte, 1024)
			defer forwardConn.Close()
			for {
				forwardConn.SetReadDeadline(time.Now().Add(5 * time.Second))
				replyLen, _, err := forwardConn.ReadFromUDP(replyBuffer)
				if err != nil {
					fmt.Println("接收第三方服务回复失败：", err)
					break
				}

				// 将第三方服务的回复发送回原始发送方
				_, err = conn.WriteToUDP(replyBuffer[:replyLen], oAddr)
				if err != nil {
					fmt.Println("发送回复到原始发送方失败：", err)
					break
				}
				fmt.Printf("消息已从第三方服务回复到原始发送方 %s\n", oAddr)
			}
		}(forwardConn, conn, origAddr)

	}
}

// SendConfig 使用TCP协议发送配置数据到指定的TCP端口
func SendConfig(tcpAddress string, configData string) string {
	// 建立TCP连接
	conn, err := net.Dial("tcp", tcpAddress)
	if err != nil {
		return fmt.Sprintf("dialing TCP failed: %v", err)
	}
	defer conn.Close()

	// 发送数据
	_, err = conn.Write([]byte(configData))
	if err != nil {
		return fmt.Sprintf("sending data failed: %v", err)
	}

	fmt.Printf("Config data sent to %s\n", tcpAddress)
	return ""
}

func PProf(address string) {
	go http.ListenAndServe(address, nil)
}

func StartCapture(path string) {
	tunhandler.WritePath = path
	tunhandler.CanCapture = true
}
func GetCaptureInfos(path string) string {
	return tunhandler.GetHostInfosString(path)
}

func HandleTun(fd, mtu int, ruleProxy string) string {
	return tunhandler.CreateFD(fd, mtu, ruleProxy)
}

func StartGTS(config string, fd int) string {
	return gts.StartGTSWith(config, fd)
}

// func StartTun2socks(tunfd int, host string, port int, mtu int, udpEnable bool, udpTimeout int) string {
// 	return client.StartTun2socks(tunfd, host, port, mtu, udpEnable, udpTimeout)
// }

// func InputPacket(packet []byte) {
// 	client.InputPacket(packet)
// }

// type PacketFlow interface {
// 	Write([]byte)
// }

// func StartTun2socksIO(flow PacketFlow, host string, port int, mtu int, udpEnable bool, udpTimeout int) string {
// 	return client.StartTun2socksIO(flow, host, port, mtu, udpEnable, udpTimeout)
// }

// client := &http.Client{}
// 	req, err := http.NewRequest("PUT", fmt.Sprintf("http://%s/configs?path=%s&force=true", externalControllerAddr, cfgPath), nil)
// 	if err != nil {
// 		fmt.Println(err)
// 		return err.Error()
// 	}
// 	req.Header = map[string][]string{
// 		"Content-Type": {"application/json"},
// 	}
// 	resp, err := client.Do(req)
// 	if err != nil {
// 		fmt.Println(err)
// 		return err.Error()
// 	}
// 	defer resp.Body.Close()
// 	return resp.Status

func main() {

}
