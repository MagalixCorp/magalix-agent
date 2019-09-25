package scalar2

import (
	"fmt"
	"strings"
	"time"

	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/api/resource"

	corev1 "k8s.io/api/core/v1"
)

const OOMKilledReason = "OOMKilled"

type OOMKillsProcessor struct {
	logger   *log.Logger
	kube     *kuber.Kube
	observer *kuber.Observer

	timeout time.Duration
	pipe    chan corev1.Pod

	dryRun bool
}

func NewOOMKillsProcessor(
	logger *log.Logger,
	kube *kuber.Kube,
	observer_ *kuber.Observer,
	timeout time.Duration,
	dryRun bool,
) *OOMKillsProcessor {
	return &OOMKillsProcessor{
		logger:   logger,
		kube:     kube,
		observer: observer_,

		timeout: timeout,
		pipe:    make(chan corev1.Pod, 1000),

		dryRun: dryRun,
	}
}

func (p *OOMKillsProcessor) Start() {
	for s := range p.pipe {
		if err := p.handlePod(s); err != nil {
			p.logger.Errorf(
				err,
				"unable to handle pod",
			)
		}
	}
}

func (p *OOMKillsProcessor) Stop() {
	close(p.pipe)
}

func (p *OOMKillsProcessor) Submit(pod corev1.Pod) error {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	select {
	case p.pipe <- pod:
		cancel()
	case <-ctx.Done():
		return karma.Format(
			karma.Describe("timeout", p.timeout).Reason(nil),
			"timeout submitting pod",
		)
	}
	return nil
}

func (p *OOMKillsProcessor) applicable(containerStatus corev1.ContainerStatus) bool {
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

func (p *OOMKillsProcessor) handlePod(pod corev1.Pod) error {

	var toChangeContainers []kuber.ContainerResourcesRequirements
	var containersDebugData []string

	for _, status := range pod.Status.ContainerStatuses {
		if p.applicable(status) {
			var container *corev1.Container
			for _, c := range pod.Spec.Containers {
				if c.Name == status.Name {
					container = &c
				}
			}
			if container == nil {
				return karma.Format(
					nil,
					"unable to find container in pod spec",
				)
			}

			currentMemLimits := container.Resources.Limits.Memory().ScaledValue(resource.Mega)
			newMemLimits := currentMemLimits * 3 / 2

			toChangeContainers = append(
				toChangeContainers,
				kuber.ContainerResourcesRequirements{
					Name: container.Name,
					Limits: kuber.RequestLimit{
						Memory: &newMemLimits,
					},
				},
			)
			containersDebugData = append(
				containersDebugData,
				fmt.Sprintf("%s:%d->%d", status.Name, currentMemLimits, newMemLimits),
			)
		}
	}

	if len(toChangeContainers) == 0 {
		return nil
	}

	parents, err := kuber.GetParents(
		&pod,
		func(kind string) (kuber.Watcher, bool) {
			gvrk, err := kuber.KindToGvrk(kind)
			if err != nil {
				return nil, false
			}
			return p.observer.Watch(*gvrk), true
		},
	)
	rootParent := kuber.RootParent(parents)

	namespace := pod.GetNamespace()
	controllerKind := rootParent.Kind
	controllerName := rootParent.Name

	ctx := karma.
		Describe("name", strings.Join([]string{namespace, controllerKind, controllerName}, "/")).
		Describe("containers", strings.Join(containersDebugData, " | ")).
		Describe("dry run", p.dryRun)

	if p.dryRun {
		//	log info about dryRun
		p.logger.Infof(ctx, "dry-run enabled, skipping OOMKill handler")
		return nil
	}

	skipped, err := p.kube.SetResources(
		controllerKind,
		controllerName,
		namespace,
		kuber.TotalResources{
			Containers: toChangeContainers,
		},
	)

	if err != nil {
		if skipped {
			p.logger.Errorf(
				ctx.Reason(err),
				"skipping OOMMKill handler execution",
			)
			return nil
		}
		return karma.Format(
			ctx.Reason(err),
			"unable to execute OOMKill handler",
		)
	}

	p.logger.Infof(ctx, "OOMKill handler executed")

	return nil

}
