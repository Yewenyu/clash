package tunhandler

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/martian/log"
)

// 上游代理地址（HOST:PORT）
var upstreamProxyAddr = "127.0.0.1:3128" // 替换成你的上游代理地址

func StartHttpCapture(localAddr, remoteAddr string) {
	upstreamProxyAddr = remoteAddr
	server := &http.Server{
		Addr:    localAddr,
		Handler: http.HandlerFunc(proxyHandler),
	}

	log.Debugf("本地代理已启动，监听 :8080，将所有请求转发到上游代理：", upstreamProxyAddr)
	err := server.ListenAndServe()
	log.Errorf("%v", err)
}

//------------------------------------------------------------------------------
// 代理 Handler
//------------------------------------------------------------------------------

func proxyHandler(w http.ResponseWriter, r *http.Request) {

	host := r.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	HandleHostInfo(host, "")
	if r.Method == http.MethodConnect {
		// 处理 CONNECT 请求（HTTPS 隧道）
		handleTunnelingToUpstream(w, r)
	} else {
		// 处理普通 HTTP 请求
		handleHTTPToUpstream(w, r)
	}
}

//------------------------------------------------------------------------------
// 处理普通 HTTP 请求 -> 上游代理
//------------------------------------------------------------------------------

func handleHTTPToUpstream(w http.ResponseWriter, r *http.Request) {
	// 我们不直接连目标地址，而是通过上游代理进行访问
	// 所以只需要在 http.Client 里设定 Proxy 即可
	// 下面演示自定义的 transport，如果你愿意也可以直接使用 http.ProxyURL(...)
	// 但这里展示了如何手动控制。

	upstreamURL, _ := url.Parse("http://" + upstreamProxyAddr)
	transport := &http.Transport{
		Proxy: http.ProxyURL(upstreamURL),
		// 其他一些可选的超时参数
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	client := &http.Client{
		Transport: transport,
	}

	// 构建一个转发给“上游代理”的请求
	// 注意：将原请求的 URL 重新指向真正的目标主机
	// 但是“发出去”的却是交给上游代理处理
	outReq := r.Clone(r.Context())
	// 由于是普通 HTTP 请求，确保 URL.Scheme 和 URL.Host 指向目标站点
	// 原始请求里 r.URL 可能是相对路径，需要我们修正
	if outReq.URL.Scheme == "" {
		outReq.URL.Scheme = "http"
	}
	if outReq.URL.Host == "" {
		outReq.URL.Host = r.Host
	}
	// 另外，必须清空 RequestURI，不然 DefaultTransport 会报错
	outReq.RequestURI = ""

	// 发给上游代理
	resp, err := client.Do(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 将上游返回的响应头写回客户端
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	// 写入响应码
	w.WriteHeader(resp.StatusCode)
	// 写入响应体
	_, _ = io.Copy(w, resp.Body)
}

//------------------------------------------------------------------------------
// 处理 CONNECT（HTTPS 隧道） -> 上游代理
//------------------------------------------------------------------------------

func handleTunnelingToUpstream(w http.ResponseWriter, r *http.Request) {
	// 1. 先与上游代理建立 TCP 连接
	//    本地代理 => 上游代理
	upstreamConn, err := net.Dial("tcp", upstreamProxyAddr)
	if err != nil {
		http.Error(w, "无法连接到上游代理: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	// 2. 向上游代理发出 CONNECT 请求，请它再去连目标服务器
	//    格式：CONNECT www.example.com:443 HTTP/1.1
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", r.Host, r.Host)
	_, err = upstreamConn.Write([]byte(req))
	if err != nil {
		http.Error(w, "向上游发送 CONNECT 失败: "+err.Error(), http.StatusServiceUnavailable)
		_ = upstreamConn.Close()
		return
	}

	// 3. 读取上游代理返回的响应，检查是否 200
	//    如果上游代理成功连上目标服务器，会返回：
	//       HTTP/1.1 200 Connection Established
	//       ...
	//    如果失败，会返回错误码
	upstreamReader := bufio.NewReader(upstreamConn)
	statusLine, err := upstreamReader.ReadString('\n')
	if err != nil {
		http.Error(w, "读取上游 CONNECT 响应失败: "+err.Error(), http.StatusServiceUnavailable)
		_ = upstreamConn.Close()
		return
	}
	if !strings.Contains(statusLine, "200") {
		// 不是 200，则把上游代理完整的响应原样转发回客户端
		// 让客户端知道 CONNECT 失败了
		headers, _ := upstreamReader.ReadString('\n')
		http.Error(w, "上游代理 CONNECT 返回非 200，状态行: "+statusLine+headers, http.StatusServiceUnavailable)
		_ = upstreamConn.Close()
		return
	} else {
		// 如果读取到 200 状态，我们还要把后面若干行头读掉，直到空行
		for {
			line, err := upstreamReader.ReadString('\n')
			if err != nil || line == "\r\n" || line == "\n" {
				break
			}
		}
	}

	// 4. 这时候，上游代理已经和目标服务器建立了隧道
	//    本地代理也需要告诉客户端 "可以开始 TLS 握手了"
	hij, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "不支持 hijacking", http.StatusInternalServerError)
		_ = upstreamConn.Close()
		return
	}
	clientConn, clientBuf, err := hij.Hijack()
	if err != nil {
		http.Error(w, "hijack 失败: "+err.Error(), http.StatusServiceUnavailable)
		_ = upstreamConn.Close()
		return
	}

	// 通知客户端，连接已建立
	// 注：真正的 TLS 握手会在 clientConn 和 upstreamConn 之间直接进行
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// 5. 开始双向复制
	//    - 把客户端发来的加密数据转发到上游代理
	//    - 把上游代理返回的数据转发回客户端
	go func() {
		defer clientConn.Close()
		defer upstreamConn.Close()
		io.Copy(upstreamConn, clientBuf)
	}()
	go func() {
		defer clientConn.Close()
		defer upstreamConn.Close()
		io.Copy(clientConn, upstreamConn)
	}()
}
