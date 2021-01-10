package gateway

import (
	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"time"
)

const (
	automationsFeedbackExpiryTime     = 30 * time.Minute
	automationsFeedbackExpiryCount    = 0
	automationsFeedbackExpiryPriority = 10
	automationsFeedbackExpiryRetries  = 5
)

func (g *MagalixGateway) SetAutomationHandler(handler agent.AutomationHandler) {
	if handler == nil {
		panic("automation handler is nil")
	}
	g.submitAutomation = handler
	g.gwClient.AddListener(proto.PacketKindAutomation, func(in []byte) ([]byte, error) {
		var automation proto.PacketAutomation
		if err := proto.DecodeSnappy(in, &automation); err != nil {
			return nil, err
		}

		containerResources := agent.ContainerResources{
			Requests: &agent.RequestLimit{
				CPU:    nil,
				Memory: nil,
			},
			Limits:   &agent.RequestLimit{
				CPU:    nil,
				Memory: nil,
			},
		}
		if automation.ContainerResources.Requests != nil {
			containerResources.Requests.CPU = automation.ContainerResources.Requests.CPU
			containerResources.Requests.Memory = automation.ContainerResources.Requests.Memory
		}
		if automation.ContainerResources.Limits != nil {
			containerResources.Limits.CPU = automation.ContainerResources.Limits.CPU
			containerResources.Limits.Memory = automation.ContainerResources.Limits.Memory
		}

		err := g.submitAutomation(&agent.Automation{
			ID:                 automation.ID,
			NamespaceName:      automation.NamespaceName,
			ControllerName:     automation.ControllerName,
			ControllerKind:     automation.ControllerKind,
			ContainerName:      automation.ContainerName,
			ContainerResources: containerResources,
		})
		if err != nil {
			errMessage := err.Error()
			return proto.EncodeSnappy(proto.PacketAutomationResponse{
				ID:    automation.ID,
				Error: &errMessage,
			})
		}

		return proto.EncodeSnappy(proto.PacketAutomationResponse{})
	})
}

func (g *MagalixGateway) SendAutomationFeedback(feedback *agent.AutomationFeedback) error {
	g.gwClient.Pipe(
		client.Package{
			Kind:        proto.PacketKindAutomationFeedback,
			ExpiryTime:  utils.After(automationsFeedbackExpiryTime),
			ExpiryCount: automationsFeedbackExpiryCount,
			Priority:    automationsFeedbackExpiryPriority,
			Retries:     automationsFeedbackExpiryRetries,
			Data: proto.PacketAutomationFeedbackRequest{
				ID:             feedback.ID,
				NamespaceName:  feedback.NamespaceName,
				ControllerName: feedback.ControllerName,
				ControllerKind: feedback.ControllerKind,
				ContainerName:  feedback.ContainerName,
				Status:         proto.AutomationStatus(feedback.Status),
				Message:        feedback.Message,
			},
		},
	)
	return nil
}
