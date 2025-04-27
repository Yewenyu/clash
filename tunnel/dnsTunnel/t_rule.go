package dnstunnel

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	C "github.com/Dreamacro/clash/constant"
	"github.com/Dreamacro/clash/log"
	R "github.com/Dreamacro/clash/rule"
	"github.com/miekg/dns"
)

var Out_tRule *TRule = CreateTRule(make([]C.Rule, 0))

type TRule struct {
	Rules     []C.Rule
	machIpMap map[string]int
	l         sync.RWMutex
}

func CreateTRule(rules []C.Rule) *TRule {
	return &TRule{
		Rules:     rules,
		machIpMap: make(map[string]int),
	}
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
	i := r.MatchCRule(meta)
	return i, i != -1
}

func (r *TRule) HandleDns(bytes []byte) error {
	if err := dns.IsMsg(bytes); err != nil {
		return err
	}

	msg := new(dns.Msg)
	if err := msg.Unpack(bytes); err != nil {
		return err
	}

	r.l.Lock()
	defer r.l.Unlock()

	var qName string
	for _, q := range msg.Question {
		qName = trimLastDot(q.Name)
		break
	}

	cM := C.Metadata{Host: qName}
	index, ok := r.Match(&cM)
	if !ok {
		return fmt.Errorf("dns not match rule")
	}

	hRule := r.Rules[index]
	if hRule.Payload() == "" {
		return nil
	}

	appendRule := func(ip string) {
		if _, exists := r.machIpMap[ip]; exists {
			return
		}

		ipRule, err := R.ParseRule("IP-CIDR", ip, hRule.Adapter(), nil)
		if err != nil {
			log.Errorln("parse IP-CIDR rule failed: %v", err)
			return
		}

		index = len(r.Rules) - 1
		last := r.Rules[index]
		r.Rules = append(r.Rules[:index], ipRule)
		r.Rules = append(r.Rules, last)
		r.machIpMap[ip] = index
	}

	for _, rr := range msg.Answer {
		switch v := rr.(type) {
		case *dns.A:
			appendRule(v.A.String() + "/32")
		case *dns.AAAA:
			appendRule(v.AAAA.String() + "/128")
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
