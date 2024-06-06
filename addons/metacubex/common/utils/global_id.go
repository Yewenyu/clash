package utils

import (
	"hash/maphash"
	"unsafe"
)

var globalSeed = maphash.MakeSeed()

func GlobalID(material string) (id [8]byte) {
	*(*uint64)(unsafe.Pointer(&id[0])) = MapHash(material)
	return
}

func MapHash(material string) uint64 {
	//maphash.go String(seed Seed, s string) uint64
	// String returns the hash of s with the given seed.
	// String is equivalent to, but more convenient and efficient than:
	var h maphash.Hash
	h.SetSeed(globalSeed)
	if _, e := h.WriteString(material); e != nil {
		panic(e)
	} else {
		return h.Sum64()
	}
}
