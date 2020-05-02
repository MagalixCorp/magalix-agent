package proc

import (
	"fmt"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/watcher"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/health-go"
	"github.com/reconquest/karma-go"
	"github.com/reconquest/stats-go"
	kbeta2 "k8s.io/api/apps/v1beta2"
	kbeta1 "k8s.io/api/batch/v1beta1"
	kapi "k8s.io/api/core/v1"
	kext "k8s.io/api/extensions/v1beta1"
	kmeta "k8s.io/apimachinery/pkg/apis/meta/v1"
	kfields "k8s.io/apimachinery/pkg/fields"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kutilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	beta2client "k8s.io/client-go/kubernetes/typed/apps/v1beta2"
	beta1batchclient "k8s.io/client-go/kubernetes/typed/batch/v1beta1"
	"k8s.io/client-go/rest"
	kcache "k8s.io/client-go/tools/cache"
)

// Observer kubernets objects observer
type Observer struct {
	clientset     *kubernetes.Clientset
	clientV1Beta2 *beta2client.AppsV1beta2Client
	batchV1Beta1  *beta1batchclient.BatchV1beta1Client
	pods          chan Pod
	replicas      chan ReplicaSpec
	health        *health.Health
	identificator Identificator

	syncer *Syncer
}

// NewObserver creates a new observer
func NewObserver(
	clientset *kubernetes.Clientset,
	clientV1Beta2 *beta2client.AppsV1beta2Client,
	batchV1Beta1 *beta1batchclient.BatchV1beta1Client,
	identificator Identificator,
	health *health.Health,
) *Observer {
	observer := &Observer{
		clientset:     clientset,
		clientV1Beta2: clientV1Beta2,
		batchV1Beta1:  batchV1Beta1,
		pods:          make(chan Pod),
		replicas:      make(chan ReplicaSpec),
		health:        health,
		identificator: identificator,
		syncer:        NewSyncer(),
	}

	return observer
}

// GetPipePods getter for observer.pods
func (observer *Observer) GetPipePods() chan Pod {
	return observer.pods
}

// GetPipeReplicas getter for observer.replicas
func (observer *Observer) GetPipeReplicas() chan ReplicaSpec {
	return observer.replicas
}

// SetSyncCallback setter for sync callback
func (observer *Observer) SetSyncCallback(fn func()) {
	observer.syncer.SetOnSync(fn)
}

// Start start the observer
func (observer *Observer) Start() {
	var mutex sync.Mutex
	var stopCh chan struct{}

	stopCh = make(chan struct{})

	kutilruntime.ErrorHandlers = append(
		kutilruntime.ErrorHandlers,
		func(err error) {
			errorf(
				err,
				"{kubernetes} handled unhandlable kubernetes/util/runtime error",
			)

			mutex.Lock()
			defer mutex.Unlock()

			select {
			case <-stopCh:
				return
			default:
				close(stopCh)
			}
		},
	)

	watchers := &sync.WaitGroup{}

	for {

		watchers.Add(1)
		go observer.watchPods(watchers, stopCh)

		watchers.Add(1)
		go observer.watchReplicationControllers(watchers, stopCh)

		watchers.Add(1)
		go observer.watchStatefulSets(watchers, stopCh)

		watchers.Add(1)
		go observer.watchDaemonSets(watchers, stopCh)

		watchers.Add(1)
		go observer.watchDeployments(watchers, stopCh)

		watchers.Add(1)
		go observer.watchReplicaSets(watchers, stopCh)

		// watchers.Add(1)
		// go observer.watchCronJobs(watchers, stopCh)

		watchers.Wait()

		mutex.Lock()
		stopCh = make(chan struct{})
		mutex.Unlock()
	}
}

func (observer *Observer) watchPods(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
) {
	observer.watch(
		watchers, stopCh,
		observer.clientset.CoreV1().RESTClient(),
		"pod",
		&kapi.Pod{},

		func(obj interface{}) {
			err := observer.handlePod(obj.(*kapi.Pod))
			if err != nil {
				errorf(err, "{kubernetes} unable to handle pod")

				observer.health.Alert(
					karma.Format(
						err,
						"kubernetes: problems with handling pods",
					),
					"watch", "pods",
				)

				stats.Increase("watch/pods/error")
			} else {
				observer.health.Resolve("watch", "pods")

				stats.Increase("watch/pods/success")
			}
		},
	)
}

func (observer *Observer) watchReplicationControllers(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
) {

	infof(nil, "{kubernetes} starting observer of replicationControllers")

	observer.watch(
		watchers,
		stopCh,
		observer.clientset.CoreV1().RESTClient(),
		"replicationcontroller",
		&kapi.ReplicationController{},

		func(obj interface{}) {
			err := observer.handleReplicationController(
				obj.(*kapi.ReplicationController),
			)
			if err != nil {
				errorf(err, "{kubernetes} unable to handle replication controller")

				observer.health.Alert(
					karma.Format(
						err,
						"kubernetes: problems with handling replication controllers",
					),
					"watch", "replicationcontrollers",
				)

				stats.Increase("watch/replicationcontrollers/error")
			} else {
				stats.Increase("watch/replicationcontrollers/success")

				observer.health.Resolve("watch", "replicationcontrollers")
			}
		},
	)
}

func (observer *Observer) watchDeployments(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
) {
	infof(nil, "{kubernetes} starting observer of deployments")

	observer.watch(
		watchers,
		stopCh,
		observer.clientset.ExtensionsV1beta1().RESTClient(),
		"deployment",
		&kext.Deployment{},

		func(obj interface{}) {
			err := observer.handleDeployment(
				obj.(*kext.Deployment),
			)
			if err != nil {
				errorf(err, "{kubernetes} unable to handle deployment")

				observer.health.Alert(
					karma.Format(
						err,
						"kubernetes: problems with handling deployments",
					),
					"watch", "deployments",
				)

				stats.Increase("watch/deployments/error")
			} else {
				stats.Increase("watch/deployments/success")

				observer.health.Resolve("watch", "deployments")
			}
		},
	)
}

func (observer *Observer) watchStatefulSets(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
) {

	infof(nil, "{kubernetes} starting observer of statefulSets")

	observer.watch(
		watchers,
		stopCh,
		observer.clientV1Beta2.RESTClient(),
		"statefulset",
		&kbeta2.StatefulSet{},

		func(obj interface{}) {
			err := observer.handleStatefulSet(
				obj.(*kbeta2.StatefulSet),
			)
			if err != nil {
				errorf(err, "{kubernetes} unable to handle statefulSet")

				observer.health.Alert(
					karma.Format(
						err,
						"kubernetes: problems with handling statefulSets",
					),
					"watch", "statefulsets",
				)

				stats.Increase("watch/statefulsets/error")
			} else {
				stats.Increase("watch/statefulsets/success")

				observer.health.Resolve("watch", "statefulsets")
			}
		},
	)
}

func (observer *Observer) handleStatefulSet(
	statefulset *kbeta2.StatefulSet,
) error {
	// specify until they fix it
	// https://github.com/kubernetes/client-go/issues/413
	statefulset.SetGroupVersionKind(schema.GroupVersionKind{
		Kind: "StatefulSet",
	})

	if observer.identificator.IsIgnored(statefulset) {
		return nil
	}

	tracef(
		karma.Describe("statefulset", logger.TraceJSON(statefulset)),
		"{kubernetes} handling statefulset",
	)

	context := karma.
		Describe("namespace", statefulset.Namespace).
		Describe("statefulset", statefulset.Name)

	id, accountID, applicationID, serviceID, err := observer.identify(statefulset)
	if err != nil {
		return context.Format(
			err,
			"unable to identify statefulset",
		)
	}

	replicas := 1
	if statefulset.Spec.Replicas != nil {
		replicas = int(*statefulset.Spec.Replicas)
	}

	observer.replicas <- ReplicaSpec{
		Name:          statefulset.Name,
		ID:            id,
		AccountID:     accountID,
		ApplicationID: applicationID,
		ServiceID:     serviceID,
		Replicas:      replicas,
	}

	return nil
}

func (observer *Observer) watchDaemonSets(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
) {

	infof(nil, "{kubernetes} starting observer of daemonSets")

	observer.watch(
		watchers,
		stopCh,
		observer.clientset.ExtensionsV1beta1().RESTClient(),
		"daemonset",
		&kext.DaemonSet{},

		func(obj interface{}) {
			err := observer.handleDaemonSet(
				obj.(*kext.DaemonSet),
			)
			if err != nil {
				errorf(err, "{kubernetes} unable to handle daemonSet")

				observer.health.Alert(
					karma.Format(
						err,
						"kubernetes: problems with handling daemonSets",
					),
					"watch", "daemonsets",
				)

				stats.Increase("watch/daemonsets/error")
			} else {
				stats.Increase("watch/daemonsets/success")

				observer.health.Resolve("watch", "daemonsets")
			}
		},
	)
}

func (observer *Observer) handleDaemonSet(
	daemonset *kext.DaemonSet,
) error {
	// specify until they fix it
	// https://github.com/kubernetes/client-go/issues/413
	daemonset.SetGroupVersionKind(schema.GroupVersionKind{
		Kind: "DaemonSet",
	})

	if observer.identificator.IsIgnored(daemonset) {
		return nil
	}

	tracef(
		karma.Describe("daemonset", logger.TraceJSON(daemonset)),
		"{kubernetes} handling daemonset",
	)

	context := karma.
		Describe("namespace", daemonset.Namespace).
		Describe("daemonset", daemonset.Name)

	id, accountID, applicationID, serviceID, err := observer.identify(daemonset)
	if err != nil {
		return context.Format(
			err,
			"unable to identify daemonset",
		)
	}

	observer.replicas <- ReplicaSpec{
		Name:          daemonset.Name,
		ID:            id,
		AccountID:     accountID,
		ApplicationID: applicationID,
		ServiceID:     serviceID,
		Replicas:      int(daemonset.Status.DesiredNumberScheduled),
	}

	return nil
}

func (observer *Observer) watch(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
	client rest.Interface,
	resource string,
	object kruntime.Object,
	process func(interface{}),
) {
	var (
		stopped = make(chan struct{})
	)

	handler := func(obj interface{}) {
		rate("kubernetes/events/" + resource).Increase()

		observer.syncer.OnHandle(
			resource,
			obj.(kmeta.Object).GetResourceVersion(),
		)

		process(obj)

		observer.syncer.OnProcess(
			resource,
			obj.(kmeta.Object).GetResourceVersion(),
		)
	}

	infof(nil, "{kubernetes} subscribed on notifications about resource: %s", resource)

	informer := observer.newInformer(
		client,
		resource,
		object,
		handler,
	)

	go func() {
		for {
			select {
			case <-stopped:
				return
			default:
			}

			synced := informer.HasSynced()
			if synced {
				version := informer.LastSyncResourceVersion()

				observer.syncer.InformResource(resource, version)

				break
			} else {
				tracef(
					nil,
					"{kubernetes} informer of %s still not synced",
					resource,
				)
			}

			time.Sleep(time.Millisecond * 100)
		}
	}()

	informer.Run(stopCh)

	close(stopped)
	watchers.Done()
}

func (observer *Observer) handlePod(pod *kapi.Pod) error {
	// specify until they fix it
	// https://github.com/kubernetes/client-go/issues/413
	pod.SetGroupVersionKind(schema.GroupVersionKind{
		Kind: "Pod",
	})

	if observer.identificator.IsIgnored(pod) {
		return nil
	}

	tracef(
		karma.Describe("pod", logger.TraceJSON(pod)),
		"{kubernetes} handling pod",
	)

	context := karma.
		Describe("namespace", fmt.Sprintf("%q", pod.Namespace)).
		Describe("pod_name", pod.Name)

	id, accountID, applicationID, serviceID, err := observer.identify(pod)
	if err != nil {
		return karma.Format(
			err,
			"unable to identify pod",
		)
	}

	containers := map[uuid.UUID]ContainerState{}
	for _, container := range pod.Status.ContainerStatuses {
		id, err := observer.identificator.GetContainerID(pod, container.Name)
		if err != nil {
			errorf(
				context.
					Describe("container_name", container.Name).
					Reason(err),
				"unable to obtain container ID",
			)
		} else {
			containers[id] = ContainerState{container.State, container.LastTerminationState}
		}
	}

	observer.pods <- Pod{
		Name:          pod.Name,
		ID:            id,
		AccountID:     accountID,
		ApplicationID: applicationID,
		ServiceID:     serviceID,
		Status:        watcher.GetStatus(string(pod.Status.Phase)),
		Containers:    containers,
	}

	return nil
}

func (observer *Observer) handleReplicationController(
	ctl *kapi.ReplicationController,
) error {
	// specify until they fix it
	// https://github.com/kubernetes/client-go/issues/413
	ctl.SetGroupVersionKind(schema.GroupVersionKind{
		Kind: "ReplicationController",
	})

	if observer.identificator.IsIgnored(ctl) {
		return nil
	}

	tracef(
		karma.Describe("ctl", logger.TraceJSON(ctl)),
		"{kubernetes} handling replication controller",
	)

	context := karma.
		Describe("namespace", ctl.Namespace).
		Describe("controller", ctl.Name)

	id, accountID, applicationID, serviceID, err := observer.identify(ctl)
	if err != nil {
		return context.Format(
			err,
			"unable to identify replication controller",
		)
	}

	replicas := 1
	if ctl.Spec.Replicas != nil {
		replicas = int(*ctl.Spec.Replicas)
	}

	observer.replicas <- ReplicaSpec{
		//Name:          ctl.Name,
		ID:            id,
		AccountID:     accountID,
		ApplicationID: applicationID,
		ServiceID:     serviceID,
		Replicas:      replicas,
	}

	return nil
}

func (observer *Observer) handleDeployment(
	deployment *kext.Deployment,
) error {
	// specify until they fix it
	// https://github.com/kubernetes/client-go/issues/413
	deployment.SetGroupVersionKind(schema.GroupVersionKind{
		Kind: "Deployment",
	})

	if observer.identificator.IsIgnored(deployment) {
		return nil
	}

	tracef(
		karma.Describe("deployment", logger.TraceJSON(deployment)),
		"{kubernetes} handling deployment",
	)

	context := karma.
		Describe("namespace", deployment.Namespace).
		Describe("deployment", deployment.Name)

	id, accountID, applicationID, serviceID, err := observer.identify(deployment)
	if err != nil {
		return context.Format(
			err,
			"unable to identify deployment",
		)
	}

	replicas := 1
	if deployment.Spec.Replicas != nil {
		replicas = int(*deployment.Spec.Replicas)
	}

	observer.replicas <- ReplicaSpec{
		Name:          deployment.Name,
		ID:            id,
		AccountID:     accountID,
		ApplicationID: applicationID,
		ServiceID:     serviceID,
		Replicas:      replicas,
	}

	return nil
}

func (observer *Observer) watchCronJobs(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
) {

	infof(nil, "{kubernetes} starting observer of cronJobs")

	observer.watch(
		watchers,
		stopCh,
		observer.batchV1Beta1.RESTClient(),
		"cronjob",
		&kbeta1.CronJob{},

		func(obj interface{}) {
			err := observer.handleCronJob(
				obj.(*kbeta1.CronJob),
			)
			if err != nil {
				errorf(err, "{kubernetes} unable to handle cronJob")

				observer.health.Alert(
					karma.Format(
						err,
						"kubernetes: problems with handling cronJobs",
					),
					"watch", "cronjobs",
				)

				stats.Increase("watch/cronjobs/error")
			} else {
				stats.Increase("watch/cronjobs/success")

				observer.health.Resolve("watch", "cronjobs")
			}
		},
	)
}

func (observer *Observer) handleCronJob(
	cronjob *kbeta1.CronJob,
) error {
	// specify until they fix it
	// https://github.com/kubernetes/client-go/issues/413
	cronjob.SetGroupVersionKind(schema.GroupVersionKind{
		Kind: "CronJob",
	})

	if observer.identificator.IsIgnored(cronjob) {
		return nil
	}

	tracef(
		karma.Describe("cronjob", logger.TraceJSON(cronjob)),
		"{kubernetes} handling cronjob",
	)

	context := karma.
		Describe("namespace", cronjob.Namespace).
		Describe("cronjob", cronjob.Name)

	id, accountID, applicationID, serviceID, err := observer.identify(cronjob)
	if err != nil {
		return context.Format(
			err,
			"unable to identify cronjob",
		)
	}

	replicas := 1
	jobSpec := cronjob.Spec.JobTemplate.Spec
	if jobSpec.Parallelism != nil && jobSpec.Completions != nil {
		if *jobSpec.Parallelism < *jobSpec.Completions {
			replicas = int(*jobSpec.Parallelism)
		} else {
			replicas = int(*jobSpec.Completions)
		}
	}

	observer.replicas <- ReplicaSpec{
		Name:          cronjob.Name,
		ID:            id,
		AccountID:     accountID,
		ApplicationID: applicationID,
		ServiceID:     serviceID,
		Replicas:      replicas,
	}

	return nil
}

func (observer *Observer) watchReplicaSets(
	watchers *sync.WaitGroup,
	stopCh chan struct{},
) {

	infof(nil, "{kubernetes} starting observer of replicaSets")

	observer.watch(
		watchers,
		stopCh,
		observer.clientV1Beta2.RESTClient(),
		"replicaset",
		&kbeta2.ReplicaSet{},

		func(obj interface{}) {
			// skip watching replica sets that are controlled of other controllers
			if rs := obj.(*kbeta2.ReplicaSet); len(rs.OwnerReferences) > 0 {
				return
			}
			err := observer.handleReplicaSet(
				obj.(*kbeta2.ReplicaSet),
			)
			if err != nil {
				errorf(err, "{kubernetes} unable to handle replicaSet")

				observer.health.Alert(
					karma.Format(
						err,
						"kubernetes: problems with handling replicaSets",
					),
					"watch", "replicasets",
				)

				stats.Increase("watch/replicasets/error")
			} else {
				stats.Increase("watch/replicasets/success")

				observer.health.Resolve("watch", "replicasets")
			}
		},
	)
}

func (observer *Observer) handleReplicaSet(
	replicaset *kbeta2.ReplicaSet,
) error {
	// specify until they fix it
	// https://github.com/kubernetes/client-go/issues/413
	replicaset.SetGroupVersionKind(schema.GroupVersionKind{
		Kind: "ReplicaSet",
	})

	if observer.identificator.IsIgnored(replicaset) {
		return nil
	}

	tracef(
		karma.Describe("replicaset", logger.TraceJSON(replicaset)),
		"{kubernetes} handling replicaset",
	)

	context := karma.
		Describe("namespace", replicaset.Namespace).
		Describe("replicaset", replicaset.Name)

	id, accountID, applicationID, serviceID, err := observer.identify(replicaset)
	if err != nil {
		return context.Format(
			err,
			"unable to identify replicaset",
		)
	}

	replicas := 1
	if replicaset.Spec.Replicas != nil {
		replicas = int(*replicaset.Spec.Replicas)
	}

	observer.replicas <- ReplicaSpec{
		Name:          replicaset.Name,
		ID:            id,
		AccountID:     accountID,
		ApplicationID: applicationID,
		ServiceID:     serviceID,
		Replicas:      replicas,
	}

	return nil
}

func (observer *Observer) identify(
	resource Identifiable,
) (string, uuid.UUID, uuid.UUID, uuid.UUID, error) {
	var (
		id            string
		accountID     uuid.UUID
		applicationID uuid.UUID
		serviceID     uuid.UUID
	)

	var err error

	id, err = observer.identificator.GetID(resource)
	if err != nil {
		return id, accountID, applicationID, serviceID, karma.Format(
			err,
			"unable to get identifier for resource",
		)
	}

	accountID, err = observer.identificator.GetAccountID(resource)
	if err != nil {
		return id, accountID, applicationID, serviceID, karma.Format(
			err,
			"unable to get account id",
		)
	}

	applicationID, err = observer.identificator.GetApplicationID(resource)
	if err != nil {
		return id, accountID, applicationID, serviceID, karma.Format(
			err,
			"unable to get application id",
		)
	}

	serviceID, err = observer.identificator.GetServiceID(resource)
	if err != nil {
		return id, accountID, applicationID, serviceID, karma.Format(
			err,
			"unable to get service id",
		)
	}

	return id, accountID, applicationID, serviceID, nil
}

func (observer *Observer) newInformer(
	client rest.Interface,
	resource string,
	objectType kruntime.Object,
	handler func(interface{}),
) kcache.Controller {
	// this code just copied from kubernetes core because they don't allow to
	// change RetryOnError field fro False to True.

	lister := kcache.NewListWatchFromClient(
		client,
		resource+"s", // should be plural
		kapi.NamespaceAll,
		kfields.Everything(),
	)

	handlers := kcache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			handler(obj)
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			handler(new)
		},
		DeleteFunc: func(obj interface{}) {
			handler(obj)
		},
	}

	clientState := kcache.NewStore(kcache.DeletionHandlingMetaNamespaceKeyFunc)

	l := sync.Mutex{}

	go func() {
		l.Lock()
		defer l.Unlock()
		if resource == "pod" {
			if res, err := lister.List(kmeta.ListOptions{}); err == nil {
				if podList, ok := res.(*kapi.PodList); ok {
					pods := podList.Items
					logger.Info(fmt.Sprintf("listing: found %d pods", len(pods)))
					for _, pod := range pods {
						// obj := pod.GetObjectMeta()
						if err := clientState.Add(&pod); err != nil {
							logger.Errorf(err, "cannot add pod to client state")
						}
						handlers.OnAdd(&pod)
					}
				} else {
					logger.Error("returned pod list is not a PodList")
				}
			} else {
				logger.Error("listing pods failed")
			}
		}
	}()

	cfg := &kcache.Config{
		Queue:            kcache.NewDeltaFIFO(kcache.MetaNamespaceKeyFunc, clientState),
		ListerWatcher:    lister,
		ObjectType:       objectType,
		FullResyncPeriod: 0,
		RetryOnError:     true,
		Process: func(obj interface{}) error {
			l.Lock()
			defer l.Unlock()
			// from oldest to newest
			for _, deltas := range obj.(kcache.Deltas) {
				switch deltas.Type {
				case kcache.Sync, kcache.Added, kcache.Updated:
					if old, exists, err := clientState.Get(deltas.Object); err == nil && exists {
						if err := clientState.Update(deltas.Object); err != nil {
							return err
						}

						handlers.OnUpdate(old, deltas.Object)
					} else {
						if err := clientState.Add(deltas.Object); err != nil {
							return err
						}

						handlers.OnAdd(deltas.Object)
					}
				case kcache.Deleted:
					if err := clientState.Delete(deltas.Object); err != nil {
						return err
					}

					handlers.OnDelete(deltas.Object)
				}
			}
			return nil
		},
	}

	informer := kcache.New(cfg)

	return informer
}
