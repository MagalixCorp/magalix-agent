package proc

import (
	"fmt"
	"runtime"
	"sync"
)

var (
	tracingLocks = true
)

func traceLock(mutex *sync.Mutex, name string) {
	if tracingLocks {
		tracef(nil, "locking mutex: %s %s", name, traceCaller())
	}
	mutex.Lock()
	if tracingLocks {
		tracef(nil, "locked mutex: %s %s", name, traceCaller())
	}
}

func traceUnlock(mutex *sync.Mutex, name string) {
	if tracingLocks {
		tracef(nil, "unlocking mutex: %s %s", name, traceCaller())
	}
	mutex.Unlock()
}

func traceCaller() string {
	_, file, no, ok := runtime.Caller(2)
	if ok {
		return fmt.Sprintf("%s:%d", file, no)
	}
	return fmt.Sprintf("[unknown caller]")
}
