package utils

import (
	"time"

	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
)

type Backoff struct {
	Sleep      time.Duration
	MaxRetries int
}

func WithBackoff(fn func() error, backoff Backoff, logger *log.Logger) error {
	if logger == nil {
		logger = stderr
	}
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

		logger.Errorf(
			karma.Describe("retry", try).Reason(err),
			"unhandled error occurred, retrying after %s",
			timeout,
		)

		time.Sleep(timeout)
	}
}
