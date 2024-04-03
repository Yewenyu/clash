package main

import (
	"io"
	"log"
	"net"
	"time"
)

func main() {

	addr := "127.0.0.1:7779"
	local := "0.0.0.0:9898"

	go ListenUDP(addr, local)
	ListenTCP(addr, local)
}

func ListenTCP(addr string, local string) {
	// 监听本地TCP端口
	listener, err := net.Listen("tcp", local)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	for {
		// 接受连接
		conn, err := listener.Accept()
		if err != nil {
			log.Print(err)
			continue
		}

		// 处理连接
		go handleTCPConnection(conn, addr)
	}
}

func handleTCPConnection(srcConn net.Conn, addr string) {
	defer srcConn.Close()

	// 连接到目标TCP服务器
	dstConn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Print(err)
		return
	}
	defer dstConn.Close()

	// 双向拷贝数据
	go io.Copy(dstConn, srcConn)
	io.Copy(srcConn, dstConn)
}

func ListenUDP(target string, local string) {
	// 监听本地UDP端口
	addr, err := net.ResolveUDPAddr("udp", local)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		handleUDPConnection(conn, target)
	}
}

func handleUDPConnection(srcConn *net.UDPConn, target string) {
	buffer := make([]byte, 2048)

	n, addr, err := srcConn.ReadFromUDP(buffer)
	if err != nil {
		log.Print(err)
		return
	}

	// 解析目标UDP地址
	dstAddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		log.Print(err)
		return
	}

	// 发送数据到目标地址
	_, err = srcConn.WriteToUDP(buffer[:n], dstAddr)
	if err != nil {
		log.Print(err)
		return
	}

	// 等待来自目标地址的回复
	srcConn.SetReadDeadline(time.Now().Add(5 * time.Second)) // 设置5秒的读取超时
	n, _, err = srcConn.ReadFromUDP(buffer)
	if err != nil {
		if err, ok := err.(net.Error); ok && err.Timeout() {
			log.Print("读取超时，未收到回复")
			return
		}
		log.Print(err)
		return
	}

	// 将数据转发回原始发送者
	_, err = srcConn.WriteToUDP(buffer[:n], addr)
	if err != nil {
		log.Print(err)
		return
	}

	log.Printf("成功转发了 %d 字节从 %v 到 %v 并回到 %v", n, addr, dstAddr, addr)
}
