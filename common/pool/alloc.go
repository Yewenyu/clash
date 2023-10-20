package pool

import (
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

	if len(pool.p) < maxPoolsPerSize {
		pool.p = append(pool.p, sync.Pool{New: func() interface{} { return make([]byte, bufferSize) }})
	}
	p := &pool.p[pool.useIndex]
	b := p.Get().([]byte)
	p.Put(b)
	buf := b[:size]
	pool.useIndex = (pool.useIndex + 1) % maxPoolsPerSize
	return buf
}

func (alloc *Allocator) Put(buf []byte) error {
	// alloc.mu.Lock()
	// defer alloc.mu.Unlock()

	// bufferSize := getBufferSize(len(buf))
	// pool, exists := alloc.buffers[bufferSize]

	// if !exists {
	// 	return errors.New("no matching pool found or all pools are empty")
	// }

	// // 检查 buf 的大小是否与池的大小匹配
	// expectedSize := bufferSize // 假设 getBufferSize 返回池的大小
	// if len(buf) != expectedSize {
	// 	return fmt.Errorf("buffer size %d does not match expected pool size %d", len(buf), expectedSize)
	// }

	// pool.useIndex = (pool.useIndex + maxPoolsPerSize - 1) % maxPoolsPerSize
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
