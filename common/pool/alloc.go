package pool

// Inspired by https://github.com/xtaci/smux/blob/master/alloc.go

import (
	"errors"
	"math/bits"
	"sync"
	"time"
)

var defaultAllocator = NewAllocator()

// Allocator for incoming frames, optimized to prevent overwriting after zeroing
type Allocator struct {
	buffers []*ObjectPool
}

// NewAllocator initiates a []byte allocator for frames less than 65536 bytes,
// the waste(memory fragmentation) of space allocation is guaranteed to be
// no more than 50%.
func NewAllocator() *Allocator {
	alloc := new(Allocator)
	alloc.buffers = make([]*ObjectPool, 17) // 1B -> 64K
	for k := range alloc.buffers {
		i := k
		alloc.buffers[k] = NewObjectPool(20, func() interface{} {
			return make([]byte, 1<<uint32(i))
		})
	}
	return alloc
}

// Get a []byte from pool with most appropriate cap
func (alloc *Allocator) Get(size int) []byte {
	switch {
	case size < 0:
		panic("alloc.Get: len out of range")
	case size == 0:
		return nil
	case size > 65536:
		return make([]byte, size)
	default:
		bits := msb(size)
		if size == 1<<bits {
			return alloc.buffers[bits].Get().([]byte)[:size]
		}

		return alloc.buffers[bits+1].Get().([]byte)[:size]
	}
}

// Put returns a []byte to pool for future use,
// which the cap must be exactly 2^n
func (alloc *Allocator) Put(buf []byte) error {
	if cap(buf) == 0 || cap(buf) > 65536 {
		return nil
	}

	bits := msb(cap(buf))
	if cap(buf) != 1<<bits {
		return errors.New("allocator Put() incorrect buffer size")
	}

	//nolint
	//lint:ignore SA6002 ignore temporarily
	alloc.buffers[bits].Put(buf)
	return nil
}

// msb return the pos of most significant bit
func msb(size int) uint16 {
	return uint16(bits.Len32(uint32(size)) - 1)
}

// ObjectPool 自定义对象池
type ObjectPool struct {
	// pool 是带缓冲的 channel，用于存储可重用的对象
	pool chan interface{}

	// newFunc 是对象创建函数
	New func() interface{}

	// maxCapacity 是池的最大容量
	maxCapacity    int
	activeTime     int64
	activeCount    int
	lock           sync.Mutex
	isHandleActive bool
}

// NewObjectPool 创建一个新的对象池
func NewObjectPool(maxCapacity int, newFunc func() interface{}) *ObjectPool {

	p := &ObjectPool{
		pool:        make(chan interface{}, maxCapacity),
		New:         newFunc,
		maxCapacity: maxCapacity,
	}
	go p.handleActive()
	return p
}

func (p *ObjectPool) handleActive() {

	p.lock.Lock()
	defer p.lock.Unlock()
	if p.isHandleActive {
		return
	}
	p.isHandleActive = true
	go func() {
		for {
			<-time.After(time.Second * 10)
			p.lock.Lock()

			if time.Now().Unix()-p.activeTime > 20 || p.activeCount < p.maxCapacity/2 {
				p.Clear()
				p.isHandleActive = false
				p.activeCount = 0
				p.lock.Unlock()
				break
			}
			p.activeCount = 0
			p.lock.Unlock()

		}
	}()

}

// Get 从池中获取一个对象
func (p *ObjectPool) Get() interface{} {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.activeTime = time.Now().Unix()

	select {
	case obj := <-p.pool:
		go p.handleActive()
		// 如果池中有对象，直接取出
		return obj
	default:
		// 如果池中无对象，调用 newFunc 创建新对象
		return p.New()
	}
}

// Put 将对象放回池中
func (p *ObjectPool) Put(obj interface{}) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.activeCount++
	select {
	case p.pool <- obj:
		// 如果池未满，放回对象
	default:
		// 如果池已满，丢弃对象（也可以选择阻塞，视需求而定）
		// fmt.Println("Pool is full, object discarded.")
	}
}

// Clear 主动清除池中所有对象
func (p *ObjectPool) Clear() {
	for {
		select {
		case <-p.pool:
			// 清空 channel 中的对象
		default:
			return
		}
	}
}
