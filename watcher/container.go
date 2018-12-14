package watcher

// StatusReason specifies the reason for container exit
type StatusReason string

const (
	// StatusReasonOOMKilled OOM killed
	StatusReasonOOMKilled StatusReason = "oom_killed"
	// StatusReasonCompleted job completed
	StatusReasonCompleted StatusReason = "completed"
	// StatusReasonError an error occurred
	StatusReasonError StatusReason = "error"
	// StatusReasonCrashLoop container in crashloop
	StatusReasonCrashLoop StatusReason = "crash_loop"
	// StatusReasonErrorImagePull cannot pull docker image
	// also includes image pull back off
	StatusReasonErrorImagePull StatusReason = "error_image_pull"
	// StatusReasonCreating creating
	StatusReasonCreating StatusReason = "creating"
	// StatusReasonErrorContainerStart error starting container
	StatusReasonErrorContainerStart StatusReason = "error_container_start"
)

// ContainerStatusSource holds info about container status
type ContainerStatusSource struct {
	ExitCode *int32       `json:"exit_code,omitempty" bson:"exit_code,omitempty"`
	Signal   *int32       `json:"signal,omitempty" bson:"signal,omitempty"`
	Reason   StatusReason `json:"reason,omitempty" bson:"reason,omitempty"`
}
