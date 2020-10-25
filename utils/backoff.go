package utils

import (
	"time"

	"github.com/MagalixTechnologies/core/logger"
	"github.com/reconquest/karma-go"
)

type Backoff struct {
	Sleep      time.Duration
	MaxRetries int
}

func WithBackoff(fn func() error, backoff Backoff) error {
	try := 0
	for {
		try++

		err := fn()
		if err == nil {
			return nil
		}

		if try > backoff.MaxRetries {
			return karma.
				Describe("retry", try).
				Describe("maxRetry", backoff.MaxRetries).
				Format(err, "max retries exceeded")
		}

		// NOTE max multiplier = 10
		// 300ms -> 600ms -> [...] -> 3000ms -> 300ms
		timeout := backoff.Sleep * time.Duration((try-1)%10+1)

		logger.Errorw(
			"unhandled error occurred",
			"retry-time", timeout,
			"error", err,
		)

		time.Sleep(timeout)
	}
}
