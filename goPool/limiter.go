package gopool

import (
	"sync"

	"github.com/alitto/pond/v2"
)

// GoroutinePool manages a pool of goroutines that can run tasks concurrently up to a set capacity.
type GoroutinePool[T any] struct {
	once        sync.Once // Ensures the startHandling method is started only once.
	tasks       chan T    // Channel for submitting tasks to be processed.
	taskHandler func(T)   // Function to handle tasks; gets called for each submitted task.
	capacity    int       // Maximum number of goroutines that can run concurrently.
	goPool      pond.Pool // Pool of goroutines that handle tasks.
}

// NewGoroutinePool creates and returns a new GoroutinePool with a specified capacity and task handler.
// The task handler is a function that will be called to process each task.
func NewGoroutinePool[T any](capacity int, taskHandler func(T)) *GoroutinePool[T] {
	return &GoroutinePool[T]{
		tasks:       make(chan T, 1), // Buffer size set to the capacity for non-blocking submissions up to capacity.
		taskHandler: taskHandler,
		capacity:    capacity,
		goPool:      pond.NewPool(capacity),
	}
}

// SetCapacity updates the capacity of the goroutine pool. This method can be used to dynamically adjust
// the number of goroutines allowed to run concurrently.
func (p *GoroutinePool[T]) SetCapacity(newCapacity int) {
	p.capacity = newCapacity
}

// startHandling continuously reads from the tasks channel and starts a new goroutine for each task.
// It uses a semaphore pattern to ensure that no more than the set capacity of goroutines run concurrently.
func (p *GoroutinePool[T]) startHandling() {
	semaphore := make(chan struct{}, p.capacity) // Semaphore to limit concurrent goroutines.

	for task := range p.tasks {
		semaphore <- struct{}{} // Acquire a slot in the semaphore.
		p.goPool.Go(func() {
			defer func() { <-semaphore }() // Release the slot when the goroutine finishes.
			p.taskHandler(task)            // Process the task.
		})
	}
}

// SubmitTask submits a task to the goroutine pool. If the pool has not been started, it starts the handling
// of tasks. The task is then sent to the tasks channel to be processed by an available goroutine.
func (p *GoroutinePool[T]) SubmitTask(task T) {
	p.once.Do(func() {
		go p.startHandling() // Ensure the handling goroutines are started only once.
	})
	p.tasks <- task // Send the task for processing.
}
