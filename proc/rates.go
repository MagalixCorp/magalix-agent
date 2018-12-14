package proc

import (
	"sync"
	"time"

	"github.com/reconquest/stats-go"
)

const (
	// AverageWindow number of points to keep
	AverageWindow = 1000
	// RateDuration duration to keep the items
	RateDuration = time.Second
)

var (
	rates     sync.Map
	latencies sync.Map
)

func rate(name string) *stats.Rate {
	item, ok := rates.Load(name)
	if !ok {
		instance := stats.NewRate(RateDuration)

		rates.Store(name, instance)

		return instance
	}

	return item.(*stats.Rate)
}

func latency(name string) *stats.AverageDuration {
	rate(name).Increase()

	item, ok := latencies.Load(name)
	if !ok {
		instance := stats.NewAverageDuration(AverageWindow)

		latencies.Store(name, instance)

		return instance
	}

	return item.(*stats.AverageDuration)
}
