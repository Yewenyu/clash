package dnstunnel

import (
	"context"
	"fmt"
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

var Out_tRule *TRule = CreateTRule(make([]C.Rule, 0))

type TRule struct {
	Rules         []C.Rule
	machIpMap     map[string]int
	l             sync.RWMutex
	handleDnsChan chan []byte
}

func CreateTRule(rules []C.Rule) *TRule {

	rule := &TRule{
		Rules:     rules,
		machIpMap: make(map[string]int),
	}
	go rule.getIpRuleFromDomain()
	return rule
}

func (r *TRule) GetRule() []C.Rule {
	rs := r.Rules
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
	_, ok, rule := r.Match(&cM)
	if !ok {
		return fmt.Errorf("dns not match rule")
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
