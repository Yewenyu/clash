package main

import (
	"bytes"
	"clash"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/Dreamacro/clash/config"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/hub"
	"github.com/Dreamacro/clash/hub/executor"
	"github.com/Dreamacro/clash/log"

	"go.uber.org/automaxprocs/maxprocs"
)

var (
	flagset            map[string]bool
	version            bool
	testConfig         bool
	homeDir            string
	configFile         string
	externalUI         string
	externalController string
	secret             string
)

func init() {
	flag.StringVar(&homeDir, "d", "", "set configuration directory")
	flag.StringVar(&configFile, "f", "", "specify configuration file")
	flag.StringVar(&externalUI, "ext-ui", "", "override external ui directory")
	flag.StringVar(&externalController, "ext-ctl", "", "override external controller address")
	flag.StringVar(&secret, "secret", "", "override secret for RESTful API")
	flag.BoolVar(&version, "v", false, "show current version of clash")
	flag.BoolVar(&testConfig, "t", false, "test configuration and exit")
	flag.Parse()

	flagset = map[string]bool{}
	flag.Visit(func(f *flag.Flag) {
		flagset[f.Name] = true
	})
}

func main() {
	maxprocs.Set(maxprocs.Logger(func(string, ...any) {}))
	if version {
		fmt.Printf("Clash %s %s %s with %s %s\n", C.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(), C.BuildTime)
		return
	}
	showlanIp()
	if homeDir != "" {
		if !filepath.IsAbs(homeDir) {
			currentDir, _ := os.Getwd()
			homeDir = filepath.Join(currentDir, homeDir)
		}
		C.SetHomeDir(homeDir)
	}
	currentPath, _ := os.Getwd() // 获取当前路径
	C.SetHomeDir(currentPath)
	// clash.SetBufferSize(1024, 1024*5)
	// clash.SetGCPrecent(20)
	clash.SetMixMaxCount(100, 70, 20)
	clash.SetBufferSize(1024, 1024*10)
	clash.DNSCachTime(300)
	// go tunnel.ListenDNS("0.0.0.0:853", "127.0.0.1:7779", "udp,tcp,doh", true, []string{"208.67.222.222", "8.8.8.8"}, []string{})
	go listenConfig()
	// go tool pprof -http=:8081 http://localhost:6060/debug/pprof/goroutine
	go http.ListenAndServe(":6060", nil)

	if configFile != "" {
		if !filepath.IsAbs(configFile) {
			currentDir, _ := os.Getwd()
			configFile = filepath.Join(currentDir, configFile)

		}
		C.SetConfig(configFile)
	} else {
		configFile = filepath.Join(C.Path.HomeDir(), C.Path.Config())
		C.SetConfig(configFile)
	}
	// clash.CustomLogFile(C.Path.HomeDir()+"/log.log", 5, 0)

	if err := config.Init(C.Path.HomeDir()); err != nil {
		log.Fatalln("Initial configuration directory error: %s", err.Error())
	}

	if testConfig {
		if _, err := executor.Parse(); err != nil {
			log.Errorln(err.Error())
			fmt.Printf("configuration file %s test failed\n", C.Path.Config())
			os.Exit(1)
		}
		fmt.Printf("configuration file %s test is successful\n", C.Path.Config())
		return
	}

	var options []hub.Option
	if flagset["ext-ui"] {
		options = append(options, hub.WithExternalUI(externalUI))
	}
	if flagset["ext-ctl"] {
		options = append(options, hub.WithExternalController(externalController))
	}
	if flagset["secret"] {
		options = append(options, hub.WithSecret(secret))
	}

	if err := hub.Parse(options...); err != nil {
		log.Fatalln("Parse config error: %s", err.Error())
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}

func listenConfig() {
	// TCP 端口和地址设置
	tcpAddress := "0.0.0.0:9876"
	controllerURL := "http://localhost:9090"
	bearerToken := ""

	// 监听 TCP
	listener, err := net.Listen("tcp", tcpAddress)
	if err != nil {
		fmt.Printf("Failed to listen on TCP port: %v\n", err)
		return
	}
	defer listener.Close()
	fmt.Println("Listening on", tcpAddress)

	for {
		// 接受连接
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Failed to accept connection: %v\n", err)
			continue
		}
		fmt.Printf("Connection accepted from %s\n", conn.RemoteAddr().String())

		go handleConnection(conn, controllerURL, bearerToken)
	}
}

func handleConnection(conn net.Conn, controllerURL string, bearerToken string) {
	defer conn.Close()

	// 缓冲区用于读取数据
	buffer := make([]byte, 20240)

	// 读取数据直到连接关闭
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			if err != io.EOF {
				fmt.Printf("Failed to read from connection: %v\n", err)
			}
			break
		}

		// 将接收到的数据写入文件
		if err := os.WriteFile(configFile, buffer[:n], 0644); err != nil {
			fmt.Printf("Failed to write to file: %v\n", err)
			continue
		}

		fmt.Printf("Config data written to configFile.txt\n")

		// 调用重新加载配置的函数
		if err := reloadConfig(controllerURL, bearerToken); err != nil {
			fmt.Printf("Error reloading configuration: %v\n", err)
		} else {
			fmt.Println("Configuration reload successful")
		}
	}
}

// reloadConfig 发送 PUT 请求到 Clash 的 /configs 接口来重新加载配置
func reloadConfig(controllerURL string, bearerToken string) error {
	// 构建请求 URL
	url := fmt.Sprintf("%s/configs", controllerURL)

	// 创建请求体，这里使用空的 JSON 对象，因为我们只是触发重新加载
	requestBody := bytes.NewReader([]byte("{}"))

	// 创建请求
	req, err := http.NewRequest("PUT", url, requestBody)
	if err != nil {
		return fmt.Errorf("creating request failed: %v", err)
	}

	// 设置认证头
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending request failed: %v", err)
	}
	defer resp.Body.Close()

	// 检查 HTTP 响应状态
	if resp.StatusCode != 200 {
		return fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	fmt.Println("Configuration reloaded successfully")
	return nil
}

func showlanIp() {
	// 获取本机的所有网络接口
	interfaces, err := net.Interfaces()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// 遍历每一个接口
	for _, iface := range interfaces {
		// 检查网络接口是否活跃并且没有被禁用
		if iface.Flags&net.FlagUp == 0 {
			continue // 接口未激活
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // 忽略环回接口
		}

		// 获取接口绑定的地址
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		// 遍历地址，找到 IPv4 地址
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			// 确保是 IPv4 地址且不是环回地址
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip != nil {
				fmt.Printf("Active interface: %v, IP: %v\n", iface.Name, ip)
			}
		}
	}
}
