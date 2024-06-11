package adapter

import (
	"fmt"
	"strings"

	"github.com/Dreamacro/clash/adapter/outbound"
	tlsC "github.com/Dreamacro/clash/addons/metacubex/component/tls"
	"github.com/Dreamacro/clash/common/structure"
	C "github.com/Dreamacro/clash/constant"
)

func ParseProxy(mapping map[string]any) (C.Proxy, error) {
	decoder := structure.NewDecoder(structure.Option{TagName: "proxy", WeaklyTypedInput: true})
	proxyType, existType := mapping["type"].(string)
	if !existType {
		return nil, fmt.Errorf("missing type")
	}

	var (
		proxy C.ProxyAdapter
		err   error
	)
	switch proxyType {
	case "ss":
		ssOption := &outbound.ShadowSocksOption{}
		err = decoder.Decode(mapping, ssOption)
		if err != nil {
			break
		}
		proxy, err = outbound.NewShadowSocks(*ssOption)
	case "ssr":
		ssrOption := &outbound.ShadowSocksROption{}
		err = decoder.Decode(mapping, ssrOption)
		if err != nil {
			break
		}
		proxy, err = outbound.NewShadowSocksR(*ssrOption)
	case "socks5":
		socksOption := &outbound.Socks5Option{}
		err = decoder.Decode(mapping, socksOption)
		if err != nil {
			break
		}
		proxy = outbound.NewSocks5(*socksOption)
	case "http":
		httpOption := &outbound.HttpOption{}
		err = decoder.Decode(mapping, httpOption)
		if err != nil {
			break
		}
		proxy = outbound.NewHttp(*httpOption)
	case "snell":
		snellOption := &outbound.SnellOption{}
		err = decoder.Decode(mapping, snellOption)
		if err != nil {
			break
		}
		proxy, err = outbound.NewSnell(*snellOption)
	case "vmess":
		if isCubexAdapter(mapping) {
			vmessOption := &outbound.VmessOptionMC{
				HTTPOpts: outbound.HTTPOptions{
					Method: "GET",
					Path:   []string{"/"},
				},
				ClientFingerprint: tlsC.GetGlobalFingerprint(),
			}

			err = decoder.Decode(mapping, vmessOption)
			if err != nil {
				break
			}
			proxy, err = outbound.NewVmessMC(*vmessOption)
		} else {
			vmessOption := &outbound.VmessOption{
				HTTPOpts: outbound.HTTPOptions{
					Method: "GET",
					Path:   []string{"/"},
				},
			}
			err = decoder.Decode(mapping, vmessOption)
			if err != nil {
				break
			}
			proxy, err = outbound.NewVmess(*vmessOption)
		}
	case "vless":
		vlessOption := &outbound.VlessOption{ClientFingerprint: tlsC.GetGlobalFingerprint()}
		err = decoder.Decode(mapping, vlessOption)
		if err != nil {
			break
		}
		proxy, err = outbound.NewVless(*vlessOption)
	case "trojan":
		if isCubexAdapter(mapping) {
			trojanOptionMc := &outbound.TrojanOptionMC{ClientFingerprint: tlsC.GetGlobalFingerprint()}
			err = decoder.Decode(mapping, trojanOptionMc)
			if err != nil {
				break
			}
			proxy, err = outbound.NewTrojanMC(*trojanOptionMc)
		} else {
			trojanOption := &outbound.TrojanOption{}
			err = decoder.Decode(mapping, trojanOption)
			if err != nil {
				break
			}
			proxy, err = outbound.NewTrojan(*trojanOption)
		}
	default:
		return nil, fmt.Errorf("unsupport proxy type: %s", proxyType)
	}

	if err != nil {
		return nil, err
	}

	return NewProxy(proxy), nil
}

func isCubexAdapter(mapping map[string]any) bool {
	adapterType, adapterTypeExist := mapping["cubex-features"].(string)
	if adapterTypeExist && strings.Compare(adapterType, "true") == 0 {
		return true
	}
	return false
}
