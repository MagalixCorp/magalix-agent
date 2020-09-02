package scalar2

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/reconquest/karma-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	corev1 "k8s.io/api/core/v1"
)

type PodProcessor interface {
	Submit(pod corev1.Pod) error
}

type ScannerListener struct {
	observer      *kuber.Observer
	podsWatcher   kuber.Watcher
	podsChan      chan corev1.Pod
	plMutex       sync.Mutex
	podsListeners []PodProcessor
}

func NewScannerListener(
	observer_ *kuber.Observer,
) *ScannerListener {
	return &ScannerListener{

		observer: observer_,
		podsChan: make(chan corev1.Pod, 0),
		plMutex:  sync.Mutex{},
	}
}

func (sl *ScannerListener) Start() {
	go sl.processPods()

	sl.podsWatcher = sl.observer.Watch(kuber.Pods)
	sl.podsWatcher.AddEventHandler(
		&kuber.ResourceEventHandlerFuncs{
			Observer: sl.observer,
			AddFunc: func(now time.Time, gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured) {
				sl.handleUnstructuredPod(&obj)
			},
			UpdateFunc: func(now time.Time, gvrk kuber.GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured) {
				sl.handleUnstructuredPod(&newObj)
			},
		},
	)
}

func (sl *ScannerListener) Stop() {
	close(sl.podsChan)
	// TODO: we may need to find a method to remove the EventHandler
}

func (sl *ScannerListener) handleUnstructuredPod(u *unstructured.Unstructured) {
	var pod corev1.Pod
	err := transcodeUnstructured(u, &pod)
	if err != nil {
		logger.Errorw(
			"unable to decode Unstructured to corev1.Pod",
			"error", err,
		)
	}
	sl.podsChan <- pod
}

func (sl *ScannerListener) AddPodListener(processor PodProcessor) {
	sl.plMutex.Lock()
	defer sl.plMutex.Unlock()

	sl.podsListeners = append(sl.podsListeners, processor)
}

func (sl *ScannerListener) processPods() {
	for pod := range sl.podsChan {
		for _, listener := range sl.podsListeners {
			err := listener.Submit(pod)
			if err != nil {
				logger.Errorw(
					"error submitting pod to listener",
					"error", err,
				)
			}
		}
	}
}

func transcodeUnstructured(
	u *unstructured.Unstructured,
	v interface{},
) error {
	b, err := u.MarshalJSON()
	if err != nil {
		return karma.Format(
			err,
			"unable to marshal Unstructured to json",
		)
	}

	err = json.Unmarshal(b, v)
	if err != nil {
		return karma.Format(
			err,
			"unable to unmarshal json into %T",
			v,
		)
	}

	return nil
}
