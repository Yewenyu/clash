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
		connChan:      make(chan ConnsQueuePoolInterface, 10),
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
		for range p.maxGo {
			go func() {
				buf := make([]byte, p.bufSize)
				for {
					conns := []ConnsQueuePoolInterface{}

					lastClearTime := time.Now()
					for {
						connsInfo = <-p.connChan
						conns = append(conns, connsInfo)
						// defer runtime.GC()
						for {
							nextConns := make([]ConnsQueuePoolInterface, 0)
							for _, connInfo := range conns {
								if connInfo.IsStop() {
									continue
								}
								if time.Now().UnixMilli()-connInfo.ActiveTime().UnixMilli() > int64(p.activeTimeout) {
									connInfo.Close()
									continue
								}
								connInfo.SetDeadline(time.Now().Add(p.deadTimeout * time.Microsecond))
								err := connInfo.Relay(buf)
								if err != nil {
									connInfo.Close()
								} else {
									nextConns = append(nextConns, connInfo)
								}
							}
							conns = nextConns
							if (len(conns) < 2 && len(p.connChan) > 0) || len(conns) == 0 {
								break
							}
							if time.Now().UnixMilli()-lastClearTime.UnixMilli() > int64(p.clearTime) && len(p.connChan) > 30 {
								lastClearTime = time.Now()
								sort.SliceIsSorted(conns, func(i, j int) bool {
									return conns[i].ActiveTime().UnixMilli() < conns[j].ActiveTime().UnixMilli()
								})
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
