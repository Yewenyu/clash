package pool

import (
	"time"
)

const (
// io.Copy default buffer size is 32 KiB
// but the maximum packet size of vmess/shadowsocks is about 16 KiB
// so define a buffer of 20 KiB to reduce the memory of each TCP relay

// RelayBufferSize uses 20KiB, but due to the allocator it will actually
// request 32Kib. Most UDPs are smaller than the MTU, and the TUN's MTU
// set to 9000, so the UDP Buffer size set to 16Kib

)

var RelayBufferSize = 20 * 1024
var UDPBufferSize = 16 * 1024

func Get(size int) []byte {
	return defaultAllocator.Get(size)
}

var last = time.Now().UnixMilli()

func Put(buf []byte) error {
	// defer func() {
	// 	current := time.Now().UnixMilli()
	// 	if current-last > 1000 {
	// 		runtime.GC()
	// 		last = current
	// 	}
	// }()
	return defaultAllocator.Put(buf)
}
