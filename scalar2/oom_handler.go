package scalar2

import (
	"fmt"
	"strings"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/reconquest/karma-go"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/api/resource"

	corev1 "k8s.io/api/core/v1"
)

const OOMKilledReason = "OOMKilled"

type OOMKillsProcessor struct {
	kube     *kuber.Kube
	observer *kuber.Observer

	timeout time.Duration
	pipe    chan corev1.Pod

	dryRun bool
}

type Pod struct {
	corev1.Pod
}

func (p *Pod) GetKind() string {
	return p.Kind
}

func (p *Pod) GetAPIVersion() string {
	return p.APIVersion
}

func NewOOMKillsProcessor(
	kube *kuber.Kube,
	observer_ *kuber.Observer,
	timeout time.Duration,
	dryRun bool,
) *OOMKillsProcessor {
	return &OOMKillsProcessor{
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
			logger.Errorw(
				"unable to handle pod",
				"error", err,
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
					Name:   container.Name,
					Limits: new(kuber.RequestLimit),
				},
			)
			toChangeContainers[0].Limits.Memory = &newMemLimits
			containersDebugData = append(
				containersDebugData,
				fmt.Sprintf("%s:%d->%d", status.Name, currentMemLimits, newMemLimits),
			)
		}
	}

	if len(toChangeContainers) == 0 {
		return nil
	}

	parents, _ := kuber.GetParents(
		&Pod{Pod: pod},
		p.observer.ParentsStore,
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

	if p.dryRun {
		//	log info about dryRun
		logger.Infow("dry-run enabled, skipping OOMKill handler",
			"name", strings.Join([]string{namespace, controllerKind, controllerName}, "/"),
			"containers", strings.Join(containersDebugData, " | "),
			"dry run", p.dryRun,
		)
		return nil
	}

	/*skipped, err := p.kube.SetResources(
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
	}*/

	logger.Infow("OOMKill handler executed",
		"name", strings.Join([]string{namespace, controllerKind, controllerName}, "/"),
		"containers", strings.Join(containersDebugData, " | "),
		"dry run", p.dryRun,
	)

	return nil

}
