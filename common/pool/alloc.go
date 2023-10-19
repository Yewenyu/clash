package pool

import (
	"errors"
	"math/bits"
	"sync"
)

const maxPoolsPerSize = 5

var defaultAllocator = NewAllocator()

type Allocator struct {
	buffers map[int]*Pool
	mu      sync.Mutex
}

type Pool struct {
	useIndex int
	p        []sync.Pool
}

func NewAllocator() *Allocator {
	return &Allocator{
		buffers: make(map[int]*Pool),
	}
}

func (alloc *Allocator) Get(size int) []byte {
	alloc.mu.Lock()
	defer alloc.mu.Unlock()

	bufferSize := getBufferSize(size)
	pool, exists := alloc.buffers[bufferSize]

	if !exists {
		pool = &Pool{
			useIndex: 0,
			p:        []sync.Pool{},
		}
		alloc.buffers[bufferSize] = pool
	}

	if pool.useIndex == len(pool.p) {
		if pool.useIndex < maxPoolsPerSize {
			pool.p = append(pool.p, sync.Pool{New: func() interface{} { return make([]byte, bufferSize) }})
		} else {
			pool.useIndex = 0
		}
	}
	p := &pool.p[pool.useIndex]
	b := p.Get().([]byte)
	p.Put(b)
	buf := b[:size]
	pool.useIndex++
	return buf
}

func (alloc *Allocator) Put(buf []byte) error {
	alloc.mu.Lock()
	defer alloc.mu.Unlock()

	bufferSize := getBufferSize(len(buf))
	pool, exists := alloc.buffers[bufferSize]

	if !exists || pool.useIndex == 0 {
		return errors.New("no matching pool found or all pools are empty")
	}

	pool.useIndex--
	return nil
}

func getBufferSize(size int) int {
	switch {
	case size <= 10:
		return 10
	case size <= 100:
		return 100
	case size <= 1000:
		return 1000
	default:
		return (size/1000 + 1) * 1000
	}
}

// msb return the pos of most significant bit
func msb(size int) uint16 {
	return uint16(bits.Len32(uint32(size)) - 1)
}
