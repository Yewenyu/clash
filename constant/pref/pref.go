package pref

import "time"

const (
	DefaultTCPTimeout = 5 * time.Second
	DefaultUDPTimeout = DefaultTCPTimeout
	DefaultTLSTimeout = DefaultTCPTimeout
)
