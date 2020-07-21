package client

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"time"

	karma "github.com/reconquest/karma-go"
)

// WaitExit waits for app exit
func (client *Client) WaitExit() {
	exitcode := <-client.exit
	client.parentLogger.Debugf(karma.Describe("code", exitcode), "program is exiting")
	os.Exit(exitcode)
}

// Recover recover from panic used to send logs before exiting
func (client *Client) Recover() {
	tears := recover()
	if tears == nil {
		return
	}

	message := fmt.Sprintf(
		"PANIC OCCURRED: %v\n%s\n", tears, string(stackTrace()),
	)
	client.Fatal(message)
}

// Done sends the exit signal
func (client *Client) Done(exitcode int, jitter bool) {
	waitTime := 0 * time.Second
	if jitter {
		// Set a random wait time of up to 600 seconds (10 minutes)
		waitTime = time.Duration(rand.Intn(600)) * time.Second
	}
	client.parentLogger.Infof(nil, "Agent will exit with status code %d in %f seconds", exitcode, waitTime.Seconds())
	time.Sleep(waitTime)
	client.exit <- exitcode
}

func stackTrace() []byte {
	buffer := make([]byte, 1024)
	for {
		stack := runtime.Stack(buffer, true)
		if stack < len(buffer) {
			return buffer[:stack]
		}
		buffer = make([]byte, 2*len(buffer))
	}
}
