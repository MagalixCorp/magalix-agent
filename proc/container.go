package proc

import "github.com/MagalixCorp/magalix-agent/v2/watcher"

// GetContainerStateStatus a helper function to get the status of the container
func GetContainerStateStatus(state ContainerState) (watcher.Status, *watcher.ContainerStatusSource) {
	var status watcher.Status
	var source *watcher.ContainerStatusSource

	switch {
	case state.Current.Running != nil:
		return watcher.StatusRunning, nil

	case state.Current.Terminated != nil:
		source = &watcher.ContainerStatusSource{
			ExitCode: &state.Current.Terminated.ExitCode,
		}

		if state.Current.Terminated.Signal > 0 {
			source.Signal = &state.Current.Terminated.Signal
		}

		reason := state.Current.Terminated.Reason

		switch {
		case reason == "Completed":
			source.Reason = watcher.StatusReasonCompleted
			status = watcher.StatusCompleted

		case reason == "OOMKilled":
			source.Reason = watcher.StatusReasonOOMKilled
			status = watcher.StatusError

		case reason == "Error":
			source.Reason = watcher.StatusReasonError
			status = watcher.StatusError
		default:
			status = watcher.StatusTerminated
		}

	case state.Current.Waiting != nil:
		reason := state.Current.Waiting.Reason
		message := state.Current.Waiting.Message

		switch {
		case reason == "CrashLoopBackOff":
			source = &watcher.ContainerStatusSource{}
			source.Reason = watcher.StatusReasonCrashLoop

			status = watcher.StatusError

		case reason == "ErrImagePull" ||
			reason == "ImagePullBackOff" ||
			reason == "InvalidImageName":

			source = &watcher.ContainerStatusSource{}
			source.Reason = watcher.StatusReasonErrorImagePull

			status = watcher.StatusError

		// @TODO (kovetskiy) find way to parse that piece of cake
		//
		// reason: rpc error: code = 2 desc = failed to start container
		// "b47403fa1c46f54c6e5ce0d83a432069e777b52d5792480688ed4576de211225":
		// Error response from daemon: {"message":"invalid header field value
		// \"oci runtime error: container_linux.go:247: starting container
		// process caused \\\"exec: \\\\\\\"/bin/app\\\\\\\": stat /bin/app: no
		// such file or directory\\\"\\n\""}
		case message == "Start Container Failed":
			source = &watcher.ContainerStatusSource{}

			source.Reason = watcher.StatusReasonErrorContainerStart
			status = watcher.StatusError

		case reason == "ContainerCreating":
			source = &watcher.ContainerStatusSource{}

			source.Reason = watcher.StatusReasonCreating
			status = watcher.StatusPending

		default:
			status = watcher.StatusPending
		}

	default:
		status = watcher.StatusUnknown
	}

	return status, source
}
