package gopool

import "github.com/alitto/pond/v2"

var Go = pond.NewPool(300)
var SubGo = pond.NewPool(100)

var GO1 = NewGoroutinePool(100, func(t func()) {
	t()
})
var SubGo1 = NewGoroutinePool(50, func(t func()) {
	t()
})
