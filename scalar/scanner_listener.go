package scalar

import (
	"github.com/MagalixCorp/magalix-agent/v2/scanner"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	kv1 "k8s.io/api/core/v1"
	"sync"
)

type ContainerProcessor interface {
	Submit(status IdentifiedContainer) error
	Applicable(status IdentifiedContainer) bool
}

type IdentifiedContainer struct {
	Container   scanner.Container
	Service     scanner.Service
	Application scanner.Application
	Status      kv1.ContainerStatus
}

type ScannerListener struct {
	logger  *log.Logger
	scanner *scanner.Scanner

	pods chan []kv1.Pod

	clMutex             sync.Mutex
	containersListeners []ContainerProcessor

	stopCh chan struct{}
}

func NewScannerListener(
	logger *log.Logger,
	scanner *scanner.Scanner,
) *ScannerListener {
	return &ScannerListener{
		logger:  logger,
		scanner: scanner,

		pods: make(chan []kv1.Pod, 0),

		clMutex: sync.Mutex{},

		stopCh: make(chan struct{}, 0),
	}
}

func (sl *ScannerListener) Start() {
	go sl.processPods()
	stop := false
	for {
		select {
		case <-sl.scanner.WaitForNextTick():
			pods, err := sl.scanner.GetPods()
			if err != nil {
				sl.logger.Errorf(err, "unable to get pods")
			}
			sl.pods <- pods
			break
		case <-sl.stopCh:
			stop = true
			break
		}
		if stop {
			break
		}
	}

	close(sl.stopCh)
	close(sl.pods)

}

func (sl *ScannerListener) Stop() {
	sl.stopCh <- struct{}{}
}

func (sl *ScannerListener) AddContainerListener(processor ContainerProcessor) {
	sl.clMutex.Lock()
	defer sl.clMutex.Unlock()

	sl.containersListeners = append(sl.containersListeners, processor)
}

func (sl *ScannerListener) processPods() {
	for pods := range sl.pods {

		for _, pod := range pods {

			for _, containerStatus := range pod.Status.ContainerStatuses {
				container, service, application, err := sl.identifyContainer(pod, containerStatus.Name)
				if err != nil {
					sl.logger.Errorf(err, "unable to obtain containerStatus ID")
				} else {
					status := IdentifiedContainer{
						Container:   *container,
						Service:     *service,
						Application: *application,
						Status:      containerStatus,
					}
					for _, listener := range sl.containersListeners {
						if listener.Applicable(status) {
							err := listener.Submit(status)
							if err != nil {
								sl.logger.Errorf(
									err,
									"error submitting to listener",
								)
							}
						}
					}
				}
			}

		}

	}
}

func (sl *ScannerListener) identifyContainer(
	pod kv1.Pod,
	containerName string,
) (*scanner.Container, *scanner.Service, *scanner.Application, error) {
	container, service, application, found := sl.scanner.FindContainerWithParents(
		pod.GetNamespace(),
		pod.GetName(),
		containerName,
	)
	if !found {
		return nil, nil, nil, karma.
			Describe("namespace", pod.GetNamespace()).
			Describe("name", pod.GetName()).
			Describe("container", containerName).
			Reason("no such container")
	}

	return container, service, application, nil
}
