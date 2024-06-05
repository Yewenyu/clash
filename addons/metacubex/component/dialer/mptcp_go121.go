//go:build go1.21

package dialer

import "net"

const multipathTCPAvailable = true

func SetMultiPathTCP(dialer *net.Dialer) {
	dialer.SetMultipathTCP(true)
}
