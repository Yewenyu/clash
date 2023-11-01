package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Dreamacro/clash/common/batch"
	C "github.com/Dreamacro/clash/constant"

	"github.com/samber/lo"
	"go.uber.org/atomic"
)

const (
	defaultURLTestTimeout = time.Second * 5
)

type HealthCheckOption struct {
	URL      string
	Interval uint
}

type HealthCheck struct {
	url       string
	proxies   []C.Proxy
	interval  uint
	lazy      bool
	lastTouch *atomic.Int64
	done      chan struct{}
}

func (hc *HealthCheck) process() {
	ticker := time.NewTicker(time.Duration(hc.interval) * time.Second)

	go hc.checkAll()
	for {
		select {
		case <-ticker.C:
			now := time.Now().Unix()
			if !hc.lazy || now-hc.lastTouch.Load() < int64(hc.interval) {
				hc.checkAll()
			} else { // lazy but still need to check not alive proxies
				notAliveProxies := lo.Filter(hc.proxies, func(proxy C.Proxy, _ int) bool {
					return !proxy.Alive()
				})
				if len(notAliveProxies) != 0 {
					hc.check(notAliveProxies)
				}
			}
		case <-hc.done:
			ticker.Stop()
			return
		}
	}
}

func (hc *HealthCheck) setProxy(proxies []C.Proxy) {
	hc.proxies = proxies
}

func (hc *HealthCheck) auto() bool {
	return hc.interval != 0
}

func (hc *HealthCheck) touch() {
	hc.lastTouch.Store(time.Now().Unix())
}

func (hc *HealthCheck) checkAll() {
	hc.check(hc.proxies)
}

var HealthCheckCallBack func(result string)

func (hc *HealthCheck) check(proxies []C.Proxy) {
	b, _ := batch.New(context.Background(), batch.WithConcurrencyNum(10))
	for _, proxy := range proxies {
		p := proxy
		b.Go(p.Name(), func() (any, error) {
			ctx, cancel := context.WithTimeout(context.Background(), defaultURLTestTimeout)
			defer cancel()
			p.URLTest(ctx, hc.url)
			return nil, nil
		})
	}
	b.Wait()
	go func(proxies []C.Proxy) {
		fast := proxies[0]
		logString := ""
		var dic = make(map[string]interface{}, 0)
		for _, proxy := range proxies {
			if fast.LastDelay() > proxy.LastDelay() {
				fast = proxy
			}
			var host = strings.Split(proxy.Addr(), ":")[0]
			dic[host] = proxy.LastDelay()
		}
		dic["fast"] = strings.Split(fast.Addr(), ":")[0]
		data, _ := json.Marshal(dic)
		var d = string(data)
		logString = fmt.Sprintf("<<url-test:%s>>", d)
		if HealthCheckCallBack != nil {
			HealthCheckCallBack(logString)
		}
	}(proxies)

}

func (hc *HealthCheck) close() {
	hc.done <- struct{}{}
}

func NewHealthCheck(proxies []C.Proxy, url string, interval uint, lazy bool) *HealthCheck {
	return &HealthCheck{
		proxies:   proxies,
		url:       url,
		interval:  interval,
		lazy:      lazy,
		lastTouch: atomic.NewInt64(0),
		done:      make(chan struct{}, 1),
	}
}
