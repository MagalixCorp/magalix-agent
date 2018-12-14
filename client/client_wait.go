package client

import (
	"fmt"
	"os"
	"runtime"

	karma "github.com/reconquest/karma-go"
)

func (client *Client) halt(reason string) {
	// debug purposes
	err := client.sendBye(reason)
	if err != nil {
		client.parentLogger.Errorf(
			err,
			"can't send bye packet",
		)
	}
}

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
func (client *Client) Done(exitcode int) {
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
