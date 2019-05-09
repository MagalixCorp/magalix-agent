package scalar

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	"golang.org/x/net/context"
)

const OOMKilledReason = "OOMKilled"

type OOMKillsProcessor struct {
	logger *log.Logger
	kube   *kuber.Kube

	timeout time.Duration
	pipe    chan IdentifiedContainer

	dryRun bool
}

func NewOOMKillsProcessor(
	logger *log.Logger,
	kube *kuber.Kube,
	timeout time.Duration,
	dryRun bool,
) *OOMKillsProcessor {
	return &OOMKillsProcessor{
		logger: logger,
		kube:   kube,

		timeout: timeout,
		pipe:    make(chan IdentifiedContainer, 1000),

		dryRun: dryRun,
	}
}

func (p *OOMKillsProcessor) Start() {
	for s := range p.pipe {
		p.handleContainer(s)
	}
}

func (p *OOMKillsProcessor) Stop() {
	close(p.pipe)
}

func (p *OOMKillsProcessor) Applicable(container IdentifiedContainer) bool {
	containerStatus := container.Status

	// if current status is OOMKilled then process it
	if containerStatus.State.Terminated != nil &&
		containerStatus.State.Terminated.Reason == OOMKilledReason {
		return true
	}

	// if the old status is OOMKilled and it was terminated one minute ago
	// and restarted 3 times at least then process it
	if containerStatus.State.Running == nil &&
		containerStatus.LastTerminationState.Terminated != nil &&
		containerStatus.LastTerminationState.Terminated.Reason == OOMKilledReason {
		return containerStatus.RestartCount > 2
	}

	return false
}

func (p *OOMKillsProcessor) Submit(container IdentifiedContainer) error {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	select {
	case p.pipe <- container:
		cancel()
	case <-ctx.Done():
		return karma.Format(
			karma.Describe("timeout", p.timeout).Reason(nil),
			"timeout submitting container",
		)
	}
	return nil
}

func (p *OOMKillsProcessor) handleContainer(status IdentifiedContainer) {
	container := status.Container
	service := status.Service
	application := status.Application

	currentMemLimits := container.Resources.SpecResourceRequirements.Limits.Memory().Value()
	// convert to Mi
	currentMemLimits = currentMemLimits / 1024 / 1024

	newMemLimits := currentMemLimits * 3 / 2

	ctx := karma.
		Describe("container", container.Name).
		Describe("container-id", container.ID).
		Describe("service", service.Name).
		Describe("service-d", service.ID).
		Describe("application", application.Name).
		Describe("application-d", application.ID).
		Describe("old value (Mi)", currentMemLimits).
		Describe("new value (Mi)", newMemLimits).
		Describe("dry run", p.dryRun)

	if p.dryRun {
		//	log info about dryRun
		p.logger.Infof(ctx, "dry-run enabled, skipping OOMKill handler")
		return
	}

	err := p.kube.SetResources(service.Kind, service.Name, application.Name, kuber.TotalResources{
		Containers: []kuber.ContainerResourcesRequirements{
			{
				Name: container.Name,
				Limits: kuber.RequestLimit{
					Memory: &newMemLimits,
				},
			},
		},
	})

	if err != nil {
		p.logger.Errorf(
			ctx.Reason(err),
			"unable to execute OOMKill handler",
		)
		return
	}

	p.logger.Infof(ctx, "OOMKill handler executed")

}
