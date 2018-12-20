package utils

import (
	"sync"
	"time"
)

type Ticker struct {
	interval time.Duration
	fn       func()

	mutex *sync.Mutex

	waitChannels []chan struct{}
}

func NewTicker(interval time.Duration, fn func()) *Ticker {
	return &Ticker{
		interval: interval,
		fn:       fn,

		mutex: &sync.Mutex{},
	}
}

func (ticker *Ticker) nextTick() <-chan time.Time {
	interval := ticker.interval
	if time.Hour%interval == 0 {
		now := time.Now()
		// TODO: sub seconds
		nanos := time.Second*time.Duration(now.Second()) + time.Minute*time.Duration(now.Minute())
		next := interval - nanos%interval
		return time.After(next)
	}
	return time.After(interval)
}

func (ticker *Ticker) unlockWaiting() {
	ticker.mutex.Lock()
	defer ticker.mutex.Unlock()
	for _, waitChan := range ticker.waitChannels {
		waitChan <- struct{}{}
		close(waitChan)
	}
	ticker.waitChannels = make([]chan struct{}, 0)
}

// Start start scanner
func (ticker *Ticker) Start(immediate, block bool) {
	ticker.mutex.Lock()
	defer ticker.mutex.Unlock()

	tickerFn := func() {
		tick := ticker.nextTick()
		for {
			<-tick

			ticker.fn()

			// unlocks routines waiting for the next tick
			ticker.unlockWaiting()
			tick = ticker.nextTick()
		}
	}

	if immediate {
		// block for first tick
		ticker.fn()
	}
	if block {
		tickerFn()
	} else {
		go tickerFn()
	}
}

// WaitForNextTick returns a signal channel that gets unblocked after the next tick
// Example usage:
//  <- ticker.WaitForNextTick()
func (ticker *Ticker) WaitForNextTick() chan struct{} {
	ticker.mutex.Lock()
	defer ticker.mutex.Unlock()
	waitChan := make(chan struct{})
	ticker.waitChannels = append(ticker.waitChannels, waitChan)
	return waitChan
}
