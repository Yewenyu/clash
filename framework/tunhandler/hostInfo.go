package tunhandler

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Dreamacro/clash/log"
)

var (
	hostMap            = make(map[string]int)
	hostInfos          = []HostInfo{}
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
}

func HandleHostInfo(host string, ip string) {
	// 将writePath的数据解析给hostInfos和hostMap

	newHostInfo := func(host string) int {

		current := HostInfo{Time: time.Now().Unix(), Host: host, AssociatedHost: []string{}, AssociatedIp: []string{}}
		hostInfos = append(hostInfos, current)
		index := len(hostInfos) - 1
		hostMap[host] = index
		return index
	}

	hostInfoOnceHandle.Do(func() {
		// err := loadHostInfoFromFile(WritePath)
		// if err != nil {
		// 	log.Debugln("[Packet Capture] err:%v", err)
		// }
		go func() {
			for {
				hosts := <-handleHostIpChan
				host := hosts[0]
				ip := hosts[1]
				var canWrite bool
				lock.Lock()
				if host != "" {
					i, found := hostMap[host]
					if !found {
						i = newHostInfo(host)
					}
					current := &hostInfos[i]
					if ip != "" {
						current.AssociatedIp = append(current.AssociatedIp, ip)
						hostMap[ip] = i
					}
					// 去重
					current.AssociatedHost = uniqueStrings(current.AssociatedHost)
					current.AssociatedIp = uniqueStrings(current.AssociatedIp)
					canWrite = true
				} else if ip != "" {
					_, found := hostMap[ip]
					if !found && !isLocalIP(ip) {
						_ = newHostInfo(ip)
						canWrite = true
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

	handleHostIpChan <- []string{host, ip}

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
func toFormattedJSON(hostInfos []HostInfo) (string, error) {
	// 使用 json.MarshalIndent 格式化 JSON，指定前缀和缩进
	formattedJSON, err := json.MarshalIndent(hostInfos, "", "  ")
	if err != nil {
		return "", err
	}

	// 转为字符串返回
	return string(formattedJSON), nil
}
func toFormattedJSONSorted(hostInfos []HostInfo) (string, error) {
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

	var loaded []HostInfo
	err = json.Unmarshal(data, &loaded)
	if err != nil {
		return err
	}

	hostInfos = loaded
	hostMap = make(map[string]int)
	for i, h := range hostInfos {
		hostMap[h.Host] = i
		for _, ah := range h.AssociatedHost {
			hostMap[ah] = i
		}
		for _, ip := range h.AssociatedIp {
			hostMap[ip] = i
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
