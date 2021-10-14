package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

func TestMagalixAgent(t *testing.T) {
	var (
		args   []string
		covurl = os.Getenv("CODECOV_URL")
	)

	if covurl == "" {
		t.Skip("codecov url is not provided")
	}

	for _, arg := range os.Args {
		switch {
		case strings.HasPrefix(arg, "-test"):
		case strings.HasPrefix(arg, "DEVEL"):
		default:
			args = append(args, arg)
		}
	}

	waitCh := make(chan int, 1)

	os.Args = args
	go func() {
		main()
		close(waitCh)
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case <-signalCh:
		return
	case <-waitCh:
		return
	}
}
