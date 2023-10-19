package clash

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"runtime/debug"

	"github.com/Dreamacro/clash/config"
	"github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/hub/executor"
	"github.com/Dreamacro/clash/log"
	"github.com/Dreamacro/clash/tunnel/statistic"

	// "github.com/eycorsican/go-tun2socks/client"

	N "github.com/Dreamacro/clash/common/net"
	"github.com/Dreamacro/clash/common/pool"
	t "github.com/Dreamacro/clash/tunnel"
)

// framework support

func ReadConfig(path string) ([]byte, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, err
	}
	data, err := ioutil.ReadFile(path)
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

func RunByConfig(configPath string, externalController string) error {
	log.Infoln("start run")
	cfgPath = configPath
	externalControllerAddr = externalController
	constant.SetConfig(configPath)
	rawConfig, err := GetRawCfgByPath(configPath)
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

func SetConnectCount(tcp int, udp int, tcpTimeout int) {
	t.SetGoCountAndTimeout(tcp, udp, tcpTimeout)
}
func ClearTcpConn() {
	t.SetClear(true)
}
func Restart() {
	// rawConfig, _ := GetRawCfgByPath(cfgPath)
	// cfg, _ := config.ParseRawConfig(rawConfig)
	// executor.ApplyConfig(cfg, true)
	t.ReStart()
}

type listenCfg struct {
	TcpCount, UdpCount, TcpSize, UdpSize, TcpTimeout, GCPercent, Memory int
	Clear, GC, ReStart                                                  bool
}

func ListenConfig(port int) {

	go func() {
		// 定义服务器的地址和端口
		serverAddress := fmt.Sprintf("127.0.0.1:%d", port)
		udpAddress, err := net.ResolveUDPAddr("udp", serverAddress)
		if err != nil {
			fmt.Println("Error: ", err)
			// os.Exit(1)
		}

		// 创建UDP连接
		conn, err := net.ListenUDP("udp", udpAddress)
		if err != nil {
			fmt.Println("Error: ", err)
			// os.Exit(1)
		}
		defer conn.Close()

		buf := make([]byte, 1024)

		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				fmt.Println("Error: ", err)
				continue
			}

			b := buf[:n]
			var j listenCfg
			err = json.Unmarshal(b, &j)
			response := ""
			if err == nil {
				if j.TcpCount > 0 {
					SetConnectCount(j.TcpCount, j.UdpCount, j.TcpTimeout)
				}
				if j.TcpSize > 0 {
					SetBufferSize(j.TcpSize, j.UdpCount)
				}
				if j.Clear {
					ClearTcpConn()
				}
				if j.GC {
					FreeOSMemory()
				}
				if j.Clear {
					ClearTcpConn()
				}
				response = fmt.Sprintf("Received %s", string(b))
			} else {
				response = fmt.Sprintf("err %s json : %s", err, string(b))
			}
			_, err = conn.WriteToUDP([]byte(response), addr)

			// 发送回应消息

			if err != nil {
				fmt.Println("Error: ", err)
				continue
			}
		}
	}()

}

// func StartTun2socks(tunfd int, host string, port int, mtu int, udpEnable bool, udpTimeout int) string {
// 	return client.StartTun2socks(tunfd, host, port, mtu, udpEnable, udpTimeout)
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
