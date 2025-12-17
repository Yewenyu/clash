package gopool

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

type ConnsQueuePoolInterface interface {
	Relay(buf []byte) error
	IsStop() bool
	Close()
	SetDeadline(deadline time.Time)
	ActiveTime() time.Time
}

type ConnsQueuePool struct {
	maxConnCount  int
	onOnce        sync.Once
	connChan      chan ConnsQueuePoolInterface
	bufSize       int
	maxGo         int
	deadTimeout   time.Duration
	activeTimeout int
	clearTime     int
}

func NewQueuePool(maxConnCount, maxGo, bufSize, activeTimeout, clearTime int, deadTimeout time.Duration) *ConnsQueuePool {

	queuePool := &ConnsQueuePool{
		maxConnCount:  maxConnCount,
		connChan:      make(chan ConnsQueuePoolInterface, maxGo),
		bufSize:       bufSize,
		maxGo:         maxGo,
		deadTimeout:   deadTimeout,
		activeTimeout: activeTimeout,
		clearTime:     clearTime,
	}
	return queuePool

}

func (p *ConnsQueuePool) AddConns(connsInfo ConnsQueuePoolInterface) {
	p.connChan <- connsInfo

	p.onOnce.Do(func() {
		for i := 0; i < p.maxGo; i++ {
			go func() {
				buf := make([]byte, p.bufSize)
				timer := time.NewTimer(100 * time.Millisecond)
				defer timer.Stop()
				for {
					conns := []ConnsQueuePoolInterface{}
					lastClearTime := time.Now()

					for {
						timer.Reset(100 * time.Millisecond)
						select {
						case info := <-p.connChan:
							conns = append(conns, info)
						case <-timer.C:
							if len(conns) == 0 {
								continue
							}
						}

						for {
							nextConns := make([]ConnsQueuePoolInterface, 0)
							for _, info := range conns {
								if info.IsStop() {
									continue
								}
								if time.Now().UnixMilli()-info.ActiveTime().UnixMilli() > int64(p.activeTimeout) {
									info.Close()
									continue
								}
								info.SetDeadline(time.Now().Add(p.deadTimeout * time.Microsecond))
								err := info.Relay(buf)
								if err != nil {
									info.Close()
								} else {
									nextConns = append(nextConns, info)
								}
							}
							conns = nextConns
							if len(conns) < p.maxConnCount/2 {
								if len(conns) == 0 {
									break
								}
								if len(p.connChan) > 0 {
									break
								}
								break
							}
							if time.Now().UnixMilli()-lastClearTime.UnixMilli() > int64(p.clearTime) && len(p.connChan) > 30 {
								lastClearTime = time.Now()
								sort.Slice(conns, func(i, j int) bool {
									return conns[i].ActiveTime().UnixMilli() < conns[j].ActiveTime().UnixMilli()
								})
								deleteConns := conns[len(conns)/2:]
								for _, info := range deleteConns {
									info.Close()
								}
								conns = conns[:len(conns)/2]
								break
							}
						}
						runtime.GC()
					}
				}
			}()
		}

	})

}
