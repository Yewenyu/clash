package dnstunnel

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Dreamacro/clash/component/resolver"
	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	R "github.com/Dreamacro/clash/rule"
	"github.com/miekg/dns"
)

var UseFileRule = true
var FileR *FileRule
var Out_tRule *TRule = CreateTRule(make([]C.Rule, 0))

type TRule struct {
	Rules         []C.Rule
	machIpMap     map[string]int
	l             sync.RWMutex
	handleDnsChan chan []byte
	IsHttpEnable  bool
}

func CreateTRule(rules []C.Rule) *TRule {

	if UseFileRule && len(rules) > 0 {
		FileR = CreateFileRule()
		rules = FileR.writeRules(rules)
	}

	rule := &TRule{
		Rules:     rules,
		machIpMap: make(map[string]int),
	}
	// go rule.getIpRuleFromDomain()
	return rule
}

func (r *TRule) SetHttpEnable(b bool) {
	r.l.Lock()
	r.IsHttpEnable = b
	r.l.Unlock()
}

func (r *TRule) GetRule() []C.Rule {
	rs := r.Rules
	ss := rs[0].Payload()
	_ = ss
	return rs
}
func (r *TRule) MatchCRule(meta *C.Metadata) (int, C.Rule) {
	rules := r.GetRule()
	for i, v := range rules {
		if v.Match(meta) {
			return i, v
		}
	}
	return -1, nil
}

func (r *TRule) Match(meta *C.Metadata) (int, bool, C.Rule) {
	i, rule := r.MatchCRule(meta)
	return i, i != -1, rule
}

func (r *TRule) getIpRuleFromDomain() {
	var domanRules = make([]C.Rule, 0)

	for _, v := range r.Rules {
		if v.RuleType() == C.Domain || v.RuleType() == C.DomainSuffix || v.RuleType() == C.DomainKeyword {
			domanRules = append(domanRules, v)
		}
	}
	if len(domanRules) == 0 {
		return
	}

	var host = make([]string, 0)
	var ruleMap = make(map[string]C.Rule)
	for _, v := range domanRules {

		switch v.RuleType() {
		case C.Domain, C.DomainSuffix:
			host = append(host, v.Payload())
			ruleMap[v.Payload()] = v
		case C.DomainKeyword:
			var suffix = []string{".com", ".cn", ".org", ".net", ".top"}
			for _, s := range suffix {
				h := v.Payload() + s
				host = append(host, h)
				ruleMap[h] = v
			}
		}
	}
	ctx := context.Background()

	type ipRule struct {
		IP   string
		Rule C.Rule
	}
	ipChan := make(chan ipRule, len(host))
	stopChan := make(chan int)
	for _, v := range host {
		var handle func(host string, retry int)
		handle = func(host string, retry int) {
			newctx, _ := context.WithTimeout(ctx, 10*time.Second)
			ip, err := resolver.LookupIP(newctx, host)
			if err != nil {
				if retry > 0 {
					handle(host, retry-1)
				}
				log.Errorln("lookup ip failed: %v", err)
				return
			}
			for _, v := range ip {
				ipChan <- ipRule{
					IP:   v.String(),
					Rule: ruleMap[host],
				}
			}
			stopChan <- 1
		}
		go handle(v, 2)
	}
	count := 0
loop:
	for {
		select {
		case <-stopChan:
			count++
			if count == len(host) {
				close(ipChan)
				close(stopChan)
				break loop
			}
		case ip := <-ipChan:
			var ipString = ip.IP
			//判断是否是ipv6
			if strings.Contains(ipString, ":") {
				ipString = ipString + "/128"
			} else {
				ipString = ipString + "/32"
			}
			r.appendIpRule(ipString, ip.Rule)
		}
	}
}

var onceHandle sync.Once

func (r *TRule) HandleDnsWithChan(bytes []byte) {
	onceHandle.Do(func() {
		r.handleDnsChan = make(chan []byte, 20)
		go func() {
			for dns := range r.handleDnsChan {
				r.HandleDns(dns)
			}
		}()
	})
	r.handleDnsChan <- bytes
}

func (r *TRule) appendIpRule(ip string, domainRule C.Rule) {

	if _, exists := r.machIpMap[ip]; exists {
		return
	}

	ipRule, err := R.ParseRule("IP-CIDR", ip, domainRule.Adapter(), nil)
	if err != nil {
		log.Errorln("parse IP-CIDR rule failed: %v", err)
		return
	}
	r.l.Lock()
	index := len(r.Rules) - 1
	last := r.Rules[index]
	r.Rules = append(r.Rules[:index], ipRule)
	r.Rules = append(r.Rules, last)
	r.machIpMap[ip] = index
	r.l.Unlock()
}
func (r *TRule) HandleDns(bytes []byte) error {
	if err := dns.IsMsg(bytes); err != nil {
		return err
	}

	msg := new(dns.Msg)
	if err := msg.Unpack(bytes); err != nil {
		return err
	}
	var qName string
	for _, q := range msg.Question {
		qName = trimLastDot(q.Name)
		break
	}
	cM := C.Metadata{Host: qName}
	var rule C.Rule
	if UseFileRule {
		rule = FileR.Match(&cM)
	}
	if rule == nil {
		_, ok, rl := r.Match(&cM)
		if !ok {
			return fmt.Errorf("dns not match rule")
		}
		rule = rl
	}

	if rule.Payload() == "" {
		return nil
	}

	for _, rr := range msg.Answer {
		switch v := rr.(type) {
		case *dns.A:
			r.appendIpRule(v.A.String()+"/32", rule)
		case *dns.AAAA:
			r.appendIpRule(v.AAAA.String()+"/128", rule)
		}
	}

	log.Debugln("[DNS handle] %s --> %s", qName, msg.Answer)
	return nil
}

func (r *TRule) getReponseDns(bytes []byte) ([]byte, bool) {
	r.l.Lock()
	defer r.l.Unlock()

	if err := dns.IsMsg(bytes); err != nil {
		return nil, true
	}

	qus := new(dns.Msg)
	if err := qus.Unpack(bytes); err != nil {
		return nil, true
	}

	if len(qus.Question) == 0 {
		return nil, true
	}

	name := fileName(qus)
	path := r.dnsFilePath(name)

	cacheBytes, err := readBytesFromFile(path)
	if err != nil {
		return nil, true
	}

	answer := new(dns.Msg)
	if err := answer.Unpack(cacheBytes); err != nil {
		return nil, true
	}

	answer = buildDNSResponseFromCache(qus, answer)
	if answer == nil || len(answer.Answer) == 0 {
		return nil, true
	}

	packed, _ := answer.Pack()

	fileInfo, err := os.Stat(path)
	if err != nil {
		return packed, true
	}

	modTime := fileInfo.ModTime()
	cTime := time.Now().Unix()
	mTime := modTime.Unix()

	if cTime-mTime > int64(DnsCachTime*2) {
		return nil, true
	}
	if cTime-mTime > int64(DnsCachTime) {
		return packed, true
	}

	log.Debugln("[DNS Cach Reponse] %s --> %s", name, answer.Answer)
	return packed, false
}

func (r *TRule) setDNSCach(bytes []byte, l *sync.Mutex) {
	msg := new(dns.Msg)
	if err := msg.Unpack(bytes); err != nil {
		return
	}

	l.Lock()
	defer l.Unlock()

	if len(msg.Answer) == 0 {
		return
	}

	qName := fileName(msg)
	dnsDir := r.dnsFilePath(qName)

	oldCach, err := readBytesFromFile(dnsDir)
	if err == nil {
		omsg := new(dns.Msg)
		if err := omsg.Unpack(oldCach); err != nil {
			return
		}
		msg.Answer = RemoveDuplicates(append(msg.Answer, omsg.Answer...), func(d dns.RR) string {
			return d.Header().Name
		})
		if bytes, err = msg.Pack(); err != nil {
			return
		}
	}

	if err := writeFileEnsureDir(dnsDir, bytes); err != nil {
		log.Errorln("[DNS Cach] file err : %v", err)
	}
}

func (r *TRule) dnsFilePath(name string) string {
	return filepath.Join(dnsDir(), name+".d")
}

func dnsDir() string {
	return filepath.Join(C.Path.HomeDir(), "DNSCach")
}

func buildDNSResponseFromCache(req *dns.Msg, cache *dns.Msg) *dns.Msg {
	if len(req.Question) == 0 || len(cache.Answer) == 0 {
		return nil
	}
	if req.Question[0].Name != cache.Answer[0].Header().Name || req.Question[0].Qtype != cache.Question[0].Qtype {
		return nil
	}

	cache.SetReply(req)
	return cache
}

func fileName(dns *dns.Msg) string {
	return fmt.Sprintf("%s_%d", dns.Question[0].Name, dns.Question[0].Qtype)
}

type FileRule struct {
	matchMap map[string]C.Rule
	lock     sync.RWMutex
}

func getRulePath() string {
	return C.Path.HomeDir() + "/dnsRule"
}

func CreateFileRule() *FileRule {
	return &FileRule{
		matchMap: make(map[string]C.Rule),
	}
}
func (r *FileRule) writeRules(rules []C.Rule) []C.Rule {
	newRules := []C.Rule{}
	//删除文件夹
	rulePath := getRulePath()
	if err := os.RemoveAll(rulePath); err != nil {
		log.Errorln("[DNS Rule] file err : %v", err)
	}
	log.Debugln("[DNS Rule] write rules at path: %s", rulePath)
	limit := make(chan int, 50)
	for _, rule := range rules {
		handleRule := func(rule C.Rule) {
			p := strings.Join(strings.Split(rule.Payload(), "."), "/") + "/" + rule.Adapter()
			var canWrite = false
			if rule.RuleType() == C.DomainSuffix || rule.RuleType() == C.DomainKeyword || rule.RuleType() == C.Domain {
				canWrite = true
				arr := strings.Split(rule.Payload(), ".")
				//翻转
				for i, j := 0, len(arr)-1; i < j; i, j = i+1, j-1 {
					arr[i], arr[j] = arr[j], arr[i]
				}
				p = strings.Join(arr, "/") + "/a_d_a_/" + rule.Adapter()
			} else if rule.RuleType() == C.IPCIDR {
				canWrite = true
			} else {
				newRules = append(newRules, rule)
			}
			if canWrite {
				var path = rulePath + "/" + p
				if err := writeFileEnsureDir(path, []byte("1")); err != nil {
					log.Errorln("[DNS Rule] file err : %v", err)
				}
			}
			<-limit
		}
		go handleRule(rule)
		limit <- 1
	}
	return newRules
}

func (r *FileRule) getCacheRule(metadata *C.Metadata) C.Rule {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var key = metadata.Host
	if key == "" {
		key = metadata.DstIP.String()
	}
	rule := r.matchMap[key]
	if rule == nil && metadata.Host != "" && metadata.DstIP != nil {
		rule = r.matchMap[metadata.DstIP.String()]
	}
	return rule
}

func (r *FileRule) Match(metadata *C.Metadata) C.Rule {
	var rule C.Rule
	rule = r.getCacheRule(metadata)
	if rule != nil {
		return rule
	}
	if metadata.Host == "" {
		ip := metadata.DstIP.String()
		rule = ipMach(ip)
	} else {
		rule = hostMath(metadata.Host)
		if rule == nil && metadata.DstIP != nil {
			rule = ipMach(metadata.DstIP.String())
		}
	}
	if rule != nil && rule.Match(metadata) {
		r.lock.Lock()
		r.matchMap[metadata.Host] = rule
		r.lock.Unlock()
	} else {
		rule = nil
	}

	return rule
}
func hostMath(host string) C.Rule {
	arr := strings.Split(host, ".")
	//翻转
	for i, j := 0, len(arr)-1; i < j; i, j = i+1, j-1 {
		arr[i], arr[j] = arr[j], arr[i]
	}
	rulePath := getRulePath()
	last := rulePath
	index := -1
	for i, v := range arr {
		path := last + "/" + v
		if _, err := os.Stat(path); err != nil {
			break
		}
		last = path
		index = i
	}
	if index == -1 {
		return nil
	}
	files, err := os.ReadDir(last + "/a_d_a_/")
	if err != nil {
		return nil
	}
	for _, file := range files {
		r, err := R.ParseRule(string(C.RuleConfigDomain), host, file.Name(), nil)
		if err == nil {
			return r
		}
	}

	return nil
}
func ipMach(ip string) C.Rule {
	//根据ip生成路径查看是否存在相关文件路径
	arr := strings.Split(ip, ".")
	rulePath := getRulePath()
	last := rulePath
	index := -1
	for i, v := range arr {
		path := last + "/" + v
		if _, err := os.Stat(path); err != nil {
			break
		}
		last = path
		index = i
	}
	if index == -1 {
		return nil
	}
	if index < len(arr) {
		for i := index + 1; i < len(arr); i++ {
			path := last + "/0"
			if _, err := os.Stat(path); err != nil {
				break
			}
			last = path
		}
	}
	//获取last路径里面所有文件路径
	files, err := os.ReadDir(last)
	if err != nil {
		return nil
	}
	ipNetString := ""
	var find = false
	for _, file := range files {
		subPath := strings.Replace(last, rulePath+"/", "", 1) + "/" + file.Name()
		ipNetString = strings.Replace(subPath, "/", ".", 3)
		ipNet, err := netip.ParsePrefix(ipNetString)
		if err != nil {
			continue
		}
		if ipNet.Contains(netip.MustParseAddr(ip)) {
			last += "/" + file.Name()
			find = true
			break
		}
	}
	if !find {
		return nil
	}
	files, err = os.ReadDir(last)
	if err != nil {
		return nil
	}
	for _, file := range files {
		r, err := R.ParseRule(string(C.RuleConfigIPCIDR), ipNetString, file.Name(), nil)
		if err == nil {
			return r
		}

	}

	return nil
}
