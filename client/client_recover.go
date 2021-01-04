package client

import (
	"fmt"
	"github.com/MagalixTechnologies/core/logger"
	"runtime"
)

// Recover recover from panic used to send logs before exiting
func (client *Client) Recover() {
	tears := recover()
	if tears == nil {
		return
	}

	message := fmt.Sprintf(
		"PANIC OCCURRED: %v\n%s\n", tears, string(stackTrace()),
	)

	logger.Fatal(message)
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
