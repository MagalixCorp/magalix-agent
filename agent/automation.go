package agent

import "context"

const (
	AutomationExecuted AutomationStatus = "executed"
	AutomationFailed   AutomationStatus = "failed"
	AutomationSkipped  AutomationStatus = "skipped"
)

type RequestLimit struct {
	CPU    *int64
	Memory *int64
}

type ContainerResources struct {
	Requests *RequestLimit
	Limits   *RequestLimit
}

type Automation struct {
	ID string

	NamespaceName  string
	ControllerName string
	ControllerKind string
	ContainerName  string

	ContainerResources ContainerResources
}

type AutomationStatus string

type AutomationFeedback struct {
	ID string

	NamespaceName  string
	ControllerName string
	ControllerKind string
	ContainerName  string

	Status  AutomationStatus
	Message string
}

type AutomationFeedbackHandler func(feedback *AutomationFeedback) error

type AutomationExecutor interface {
	Start(ctx context.Context) error
	Stop() error
	SubmitAutomation(automation *Automation) error
	SetAutomationFeedbackHandler(handler AutomationFeedbackHandler)
}
