package proc

import (
	"github.com/MagalixCorp/magalix-agent/v2/watcher"
	"github.com/MagalixTechnologies/core/logger"
)

// GetPodStatus a helper function to get the status of a pod
func GetPodStatus(pod Pod) watcher.Status {
	lg := logger.With(
		"application_id", pod.ApplicationID,
		"service_id", pod.ServiceID,
		"kubernetes/status", pod.Status.String,
	)

	if pod.Status == watcher.StatusTerminated {
		lg.Debugf(
			"pod: %s (%s) status: %s",
			pod.ID,
			pod.Name,
			pod.Status.String(),
		)

		return pod.Status
	}

	var running int
	var pending int
	var completed int
	var errors int

	for container, state := range pod.Containers {
		// handle case when all container terminated
		status, _ := GetContainerStateStatus(state)

		switch {
		case status == watcher.StatusRunning:
			running++
		case status == watcher.StatusPending:
			pending++
		case status == watcher.StatusCompleted:
			completed++
		case status == watcher.StatusUnknown:
			lg.Warnf(
				"container: %s unknown status, proceeding as error anyway",
				container,
			)
			fallthrough

		default:
			errors++
		}
	}

	total := len(pod.Containers)

	lg = lg.With(
		"containers/running", running,
		"containers/pending", pending,
		"containers/completed", completed,
		"containers/errors", errors,
		"containers/total", total,
	)

	newStatus := pod.Status
	switch {
	case errors > 0:
		newStatus = watcher.StatusError

	case pending > 0:
		newStatus = watcher.StatusPending

	case completed == total && total > 0:
		newStatus = watcher.StatusCompleted

	case running == total && total > 0:
		newStatus = watcher.StatusRunning

	case running > 0 && completed > 0 && running+completed == total:
		newStatus = watcher.StatusRunning
	}

	lg.Debugf(
		"pod: %s (%s) status: %s",
		pod.ID, pod.Name, newStatus.String(),
	)

	return newStatus
}
