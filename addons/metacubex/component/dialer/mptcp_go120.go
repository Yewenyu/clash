//go:build !go1.21

package dialer

import (
	"net"
)

const multipathTCPAvailable = false

func SetMultiPathTCP(dialer *net.Dialer) {
}
