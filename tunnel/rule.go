package tunnel

import (
	"sort"
	"sync"
	"time"

	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	R "github.com/Dreamacro/clash/rule"
	"github.com/miekg/dns"
)

var (
	DnsCachSize = 100
)

type TRule struct {
	Rules     []C.Rule
	domainMap map[string]int
	dnsMap    map[string]string
	dnsCach   map[string]DNSCach
	l         sync.Mutex
}
type DNSCach struct {
	msg  dns.Msg
	time int64
	name string
}

func CreateTRule(rules []C.Rule) *TRule {
	tr := &TRule{}
	tr.Rules = rules
	tr.domainMap = make(map[string]int)
	tr.dnsMap = make(map[string]string)
	tr.dnsCach = make(map[string]DNSCach)
	return tr
}

func (r *TRule) MatchCRule(meta *C.Metadata) int {
	for i, v := range r.Rules {
		if v.Match(meta) {
			return i
		}
	}
	return -1
}
func (r *TRule) Match(meta *C.Metadata) (int, bool) {
	index, exist := -1, false
	if meta.Host != "" {
		index, exist = r.domainMap[meta.Host]
	}
	handleMap := func() (int, bool) {
		index := r.MatchCRule(meta)
		if index != -1 {
			if index != len(r.Rules)-1 {
				var v = meta.Host
				if v == "" {
					v = meta.DstIP.String()
				}
				r.domainMap[v] = index
			}
			return index, true
		}
		return index, false
	}
	if !exist {
		index, exist = r.domainMap[meta.DstIP.String()]
		if !exist {
			host, exist := r.dnsMap[meta.DstIP.String()]
			log.Debugln("[TRULE] dnsMap %s --> %s", meta.DstIP.String(), host)
			if exist {
				index, exist = r.domainMap[host]
				if !exist {
					handle := func() {}
					if meta.Host == "" {
						meta.Host = host
						handle = func() {
							meta.Host = ""
						}
					}
					_, _ = handleMap()
					handle()
					index, exist = r.domainMap[host]
				}
				if exist {
					hRule := r.Rules[index]

					payload := meta.DstIP.String()
					if meta.DstIP.To4() == nil {
						payload += "/128"
					} else {
						payload += "/32"
					}
					ipRule, err := R.ParseRule("IP-CIDR", payload, hRule.Adapter(), nil)
					if err == nil {
						r.l.Lock()
						index = len(r.Rules) - 1
						last := r.Rules[index]
						r.domainMap[meta.DstIP.String()] = index
						r.Rules = append(r.Rules[:index], ipRule)
						r.Rules = append(r.Rules, last)
						r.l.Unlock()
						return index, true
					}
				}
			}
		}
	}
	if !exist {
		return handleMap()
	}
	return index, exist

}

func (r *TRule) HandleDns(bytes []byte) {
	err := dns.IsMsg(bytes)
	if err != nil {
		return
	}
	msg := new(dns.Msg)
	_ = msg.Unpack(bytes)

	r.l.Lock()
	defer r.l.Unlock()
	var qName = ""
	for _, q := range msg.Question {
		log.Debugln("[DNS] Question %s ", q.Name)
		qName = trimLastDot(q.Name)
	}
	if len(msg.Answer) > 0 {
		if len(r.dnsCach) > DnsCachSize {
			caches := make([]DNSCach, 0)
			for _, v := range r.dnsCach {
				caches = append(caches, v)
			}
			sort.Slice(caches, func(i, j int) bool {
				return caches[i].time < caches[j].time
			})
			caches = caches[:len(caches)/2]
			for _, v := range caches {
				delete(r.dnsCach, v.name)
			}
		}
		r.dnsCach[qName] = DNSCach{msg: *msg, time: time.Now().UnixMilli(), name: qName}
	}

	for _, rr := range msg.Answer {
		switch v := rr.(type) {
		case *dns.A:
			r.dnsMap[v.A.String()] = qName
			log.Debugln("[DNS] %s --> %s", qName, v.A.String())
		case *dns.AAAA:
			r.dnsMap[v.AAAA.String()] = qName
			log.Debugln("[DNS] %s --> %s", qName, v.AAAA.String())
		}
	}
}
func (r *TRule) GetReponseDns(bytes []byte) []byte {
	err := dns.IsMsg(bytes)
	if err != nil {
		return nil
	}
	qus := new(dns.Msg)
	_ = qus.Unpack(bytes)
	if len(qus.Question) > 0 {
		name := trimLastDot(qus.Question[0].Name)
		cach, exist := r.dnsCach[name]
		if exist {
			answer := cach.msg
			answer.SetReply(qus)
			bytes, _ := answer.Pack()
			return bytes
		}

	}
	return nil
}

// trimLastDot 移除字符串最后的点（如果存在）
func trimLastDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1] // 返回除了最后一个字符以外的所有字符
	}
	return s // 如果没有最后的点，直接返回原字符串
}
