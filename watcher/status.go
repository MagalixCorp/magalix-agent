package watcher

import (
	"strings"
)

// Status entity status
type Status int // this should be int64 because json can return only int64

var (
	// StatusUnknown fallback status
	StatusUnknown Status // 0
	// StatusRunning running
	StatusRunning Status = 1
	// StatusPending pending
	StatusPending Status = 2
	// StatusWarning warning
	StatusWarning Status = 3
	// StatusError error
	StatusError Status = 4
	// StatusStopping stopping
	StatusStopping Status = 5
	// StatusStopped stopped
	StatusStopped Status = 6
	// StatusTerminating terminating
	StatusTerminating Status = 7
	// StatusTerminated terminated
	StatusTerminated Status = 8
	// StatusPaused paused
	StatusPaused Status = 9
	// StatusCompleted completed (for jobs)
	StatusCompleted Status = 10
)

func (status Status) String() string {
	switch status {
	case 0:
		return "unknown"
	case 1:
		return "running"
	case 2:
		return "pending"
	case 3:
		return "warning"
	case 4:
		return "error"
	case 5:
		return "stopping"
	case 6:
		return "stopped"
	case 7:
		return "terminating"
	case 8:
		return "terminated"
	case 9:
		return "paused"
	case 10:
		return "completed"

	}

	return "unknown"
}

// GetStatus get status from string
func GetStatus(representation string) Status {
	switch strings.ToLower(representation) {
	case "unknown":
		return StatusUnknown
	case "running":
		return StatusRunning
	case "pending":
		return StatusPending
	case "warning":
		return StatusWarning
	case "error":
		return StatusError
	case "stopping":
		return StatusStopping
	case "stopped":
		return StatusStopped
	case "terminating":
		return StatusTerminating
	case "terminated":
		return StatusTerminated
	case "paused":
		return StatusPaused
	case "completed":
		return StatusCompleted

	}

	return StatusUnknown
}
