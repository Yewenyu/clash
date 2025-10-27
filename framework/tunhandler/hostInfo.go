package tunhandler

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Dreamacro/clash/log"
)

var (
	hostMap            = make(map[string]*HostInfo)
	hostInfos          = []*HostInfo{}
	handleHostIpChan   = make(chan []string, 10)
	hostInfoOnceHandle sync.Once
	WritePath          string
	lock               sync.Mutex
)

type HostInfo struct {
	Time           int64    `json:"time"`
	Host           string   `json:"host"`
	AssociatedHost []string `json:"associatedHost"`
	AssociatedIp   []string `json:"associatedIp"`
	Proto          []string `json:"proto"`
}

func HandleHostInfo(host string, ip string, proto string) {
	// 将writePath的数据解析给hostInfos和hostMap

	newHostInfo := func(host, proto string) *HostInfo {

		current := &HostInfo{Time: time.Now().Unix(), Host: host, AssociatedHost: []string{}, AssociatedIp: []string{}}
		if proto != "" {
			current.Proto = []string{proto}
		}

		hostInfos = append(hostInfos, current)
		hostMap[host] = current
		return current
	}

	hostInfoOnceHandle.Do(func() {

		go func() {
			for {
				hosts := <-handleHostIpChan
				host := hosts[0]

				ip := hosts[1]
				proto := hosts[2]
				var canWrite bool
				lock.Lock()
				if host != "" {
					hostSuffix, err := ExtractDomain(host)
					if err != nil {
						hostSuffix = host
					}
					current, found := hostMap[hostSuffix]
					if !found {
						current = newHostInfo(hostSuffix, proto)
					}
					// current := &hostInfos[i]
					if proto != "" {
						current.Proto = append(current.Proto, proto)
						current.Proto = uniqueStrings(current.Proto)
					}
					current.AssociatedHost = append(current.AssociatedHost, host)

					if ip != "" {
						current.AssociatedIp = append(current.AssociatedIp, ip)
						_, found = hostMap[ip]
						if !found {
							hostMap[ip] = current
						}

					}
					// 去重
					current.AssociatedHost = uniqueStrings(current.AssociatedHost)
					current.AssociatedIp = uniqueStrings(current.AssociatedIp)
					canWrite = true
				} else if ip != "" {
					current, found := hostMap[ip]
					if !found && !isLocalIP(ip) {
						_ = newHostInfo(ip, proto)
						canWrite = true
					}
					if found && proto != "" {
						if !stringInSlice(proto, current.Proto) {
							current.Proto = append(current.Proto, proto)
							canWrite = true
						}
					}
				}

				if canWrite {
					// 把hostInfos写进writePath
					err := saveHostInfoToFile(WritePath)
					if err != nil {
						log.Debugln("[Packet Capture]can write %s err:%v", WritePath, err)
					}

				}
				lock.Unlock()
			}
		}()
	})

	handleHostIpChan <- []string{host, ip, proto}

}

func ExtractDomain(rawURL string) (string, error) {
	// 如果输入没有协议前缀，添加一个临时的
	if !strings.Contains(rawURL, "://") && !strings.HasPrefix(rawURL, "//") {
		rawURL = "http://" + rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("cannot extract host from URL")
	}

	// 处理端口号
	host = strings.Split(host, ":")[0]

	return ExtractPrimaryDomain(host)
}

// 方法2：直接处理域名字符串
func ExtractPrimaryDomain(domain string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("empty domain")
	}

	// 转换为小写
	domain = strings.ToLower(domain)

	// 移除 www 前缀
	domain = strings.TrimPrefix(domain, "www.")

	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid domain: %s", domain)
	}

	// 处理常见情况
	switch len(parts) {
	case 2:
		// example.com
		return domain, nil
	case 3:
		// 处理 co.uk, com.cn 等双后缀域名
		if isDoubleSuffix(parts[1] + "." + parts[2]) {
			if len(parts) >= 3 {
				return strings.Join(parts[len(parts)-3:], "."), nil
			}
		}
		// sub.example.com → example.com
		return strings.Join(parts[1:], "."), nil
	default:
		// 处理多级子域名 sub.sub.example.com → example.com
		if isDoubleSuffix(parts[len(parts)-2] + "." + parts[len(parts)-1]) {
			return strings.Join(parts[len(parts)-3:], "."), nil
		}
		return strings.Join(parts[len(parts)-2:], "."), nil
	}
}

// 常见的双后缀域名
var doubleSuffixes = map[string]bool{
	"co.uk": true, "com.uk": true, "org.uk": true, "net.uk": true,
	"ac.uk": true, "gov.uk": true, "co.jp": true, "com.au": true,
	"net.au": true, "org.au": true, "com.cn": true, "net.cn": true,
	"org.cn": true, "gov.cn": true, "co.nz": true, "co.kr": true,
	"co.il": true, "co.in": true, "com.sg": true, "com.tw": true,
	"com.hk": true, "com.mx": true, "com.br": true,
}

func isDoubleSuffix(suffix string) bool {
	return doubleSuffixes[suffix]
}

// 方法3：使用正则表达式（简单情况）
func ExtractDomainRegex(input string) (string, error) {
	// 匹配域名格式
	re := regexp.MustCompile(`(?:https?://)?(?:www\.)?([a-zA-Z0-9-]+\.[a-zA-Z0-9-.]+)`)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 2 {
		return "", fmt.Errorf("no domain found")
	}

	return ExtractPrimaryDomain(matches[1])
}

func stringInSlice(s string, slice []string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// uniqueStrings 去除字符串切片中重复的元素，保持顺序不变
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

func GetHostInfos(path string) []HostInfo {
	info, err := os.Stat(path)
	if err != nil {
		// 文件不存在或无法访问时，返回空切片
		return []HostInfo{}
	}
	if info.Size() == 0 {
		// 文件为空，返回空切片
		return []HostInfo{}
	}

	f, err := os.Open(path)
	if err != nil {
		// 打开文件失败，返回空切片
		return []HostInfo{}
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		// 读取文件失败，返回空切片
		return []HostInfo{}
	}

	var loaded []HostInfo
	err = json.Unmarshal(data, &loaded)
	if err != nil {
		// JSON解析失败，返回空切片
		return []HostInfo{}
	}

	return loaded
}

func GetHostInfosString(path string) string {
	// 使用 GetHostInfos 获取数据
	his := GetHostInfos(path)
	if len(his) == 0 {
		// 无数据返回空字符串
		return ""
	}

	// 将数据序列化为JSON字符串返回
	b, err := json.Marshal(his)
	if err != nil {
		// 序列化失败，返回空字符串
		return ""
	}
	return string(b)
}

// 提取不重复的值
func ExtractUnique(hostInfos []HostInfo) []string {
	uniqueSet := make(map[string]struct{}) // 使用 map 去重
	for _, info := range hostInfos {
		uniqueSet[info.Host] = struct{}{} // 添加 Host
		for _, h := range info.AssociatedHost {
			uniqueSet[h] = struct{}{} // 添加 AssociatedHost
		}
		for _, ip := range info.AssociatedIp {
			uniqueSet[ip] = struct{}{} // 添加 AssociatedIp
		}
	}

	// 将 map 转为 slice
	var uniqueArray []string
	for key := range uniqueSet {
		uniqueArray = append(uniqueArray, key)
	}
	sort.Strings(uniqueArray)

	return uniqueArray
}
func toFormattedJSON(hostInfos []*HostInfo) (string, error) {
	// 使用 json.MarshalIndent 格式化 JSON，指定前缀和缩进
	formattedJSON, err := json.MarshalIndent(hostInfos, "", "  ")
	if err != nil {
		return "", err
	}

	// 转为字符串返回
	return string(formattedJSON), nil
}
func toFormattedJSONSorted(hostInfos []*HostInfo) (string, error) {
	// 先排序
	sort.Slice(hostInfos, func(i, j int) bool {
		return hostInfos[i].Time > hostInfos[j].Time
	})
	// 再格式化 JSON
	return toFormattedJSON(hostInfos)
}
func toBytes() ([]byte, error) {
	s, err := toFormattedJSONSorted(hostInfos)
	return []byte(s), err
}

// 保存hostInfos到文件
func saveHostInfoToFile(path string) error {

	bytes, err := toBytes()
	if err != nil {
		return err
	}
	// 打开或创建文件
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer file.Close()

	// 写入数据到文件
	if _, err := file.Write(bytes); err != nil {
		return err
	}

	return err
}

// 从文件加载数据到hostInfos和hostMap
func loadHostInfoFromFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		// 文件为空，无需加载
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}

	var loaded []*HostInfo
	err = json.Unmarshal(data, &loaded)
	if err != nil {
		return err
	}

	hostInfos = loaded
	hostMap = make(map[string]*HostInfo)
	for _, h := range hostInfos {
		hostMap[h.Host] = h
		for _, ah := range h.AssociatedHost {
			hostMap[ah] = h
		}
		for _, ip := range h.AssociatedIp {
			hostMap[ip] = h
		}
	}
	return nil
}

// 判断是否为本地 IP
func isLocalIP(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		// 无效 IP
		return false
	}

	// 检查是否为 0.0.0.0
	if parsedIP.IsUnspecified() {
		return true // 0.0.0.0 表示本地的所有网络接口
	}

	// 检查是否为回环地址（127.0.0.0/8）
	if parsedIP.IsLoopback() {
		return true
	}

	// 检查是否为私有地址（10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16）
	if parsedIP.IsPrivate() {
		return true
	}

	// 检查是否为链路本地地址（169.254.0.0/16）
	if parsedIP.IsLinkLocalUnicast() {
		return true
	}

	return false
}
