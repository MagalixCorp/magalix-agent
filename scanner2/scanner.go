package scanner2

import (
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	deltasBufferChanSize       = 1024
	deltasPacketFlushAfterSize = 100
	deltasPacketFlushAfterTime = time.Second * 10

	deltasPacketExpireAfter = time.Hour
	deltasPacketExpireCount = 0
	deltasPacketPriority    = 1
	deltasPacketRetries     = 5

	resyncPacketExpireAfter = time.Hour
	resyncPacketExpireCount = 2
	resyncPacketPriority    = 2
	resyncPacketRetries     = 5
)

var (
	Nodes = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("nodes"),
		Kind:                 "Node",
	}
	Namespaces = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("namespaces"),
		Kind:                 "Namespace",
	}
	Pods = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("pods"),
		Kind:                 "Pod",
	}
	ReplicationControllers = GroupVersionResourceKind{
		GroupVersionResource: corev1.SchemeGroupVersion.WithResource("replicationcontrollers"),
		Kind:                 "ReplicationController",
	}

	Deployments = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("deployments"),
		Kind:                 "Deployment",
	}
	StatefulSets = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("statefulsets"),
		Kind:                 "StatefulSet",
	}
	DaemonSets = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("daemonsets"),
		Kind:                 "DaemonSet",
	}
	ReplicaSets = GroupVersionResourceKind{
		GroupVersionResource: appsv1.SchemeGroupVersion.WithResource("replicasets"),
		Kind:                 "ReplicaSet",
	}

	Jobs = GroupVersionResourceKind{
		GroupVersionResource: batchv1.SchemeGroupVersion.WithResource("jobs"),
		Kind:                 "Job",
	}
	CronJobs = GroupVersionResourceKind{
		GroupVersionResource: batchv1beta1.SchemeGroupVersion.WithResource("cronjobs"),
		Kind:                 "CronJob",
	}
)

var (
	watchedResources = []GroupVersionResourceKind{
		Nodes,
		Namespaces,
		//LimitRanges,
		Pods,
		ReplicationControllers,
		Deployments,
		StatefulSets,
		DaemonSets,
		ReplicaSets,
		Jobs,
		CronJobs,
	}
)

//func kindToGvrk(kind string) (GroupVersionResourceKind, error) {
//	for _, watchedResource := range watchedResources {
//		if watchedResource.Kind == kind {
//			return watchedResource, nil
//		}
//	}
//	return GroupVersionResourceKind{}, karma.Format(
//		nil,
//		"unable to get GVR from kind: %s",
//		kind,
//	)
//}

type EntitiesWatcher interface {
	Start()
}

type entitiesWatcher struct {
	logger   *log.Logger
	client   *client.Client
	observer *Observer

	watchers          map[GroupVersionResourceKind]Watcher
	watchersByKind    map[string]Watcher
	deltasQueue       chan proto.PacketEntityDelta
	flushDeltasChan   chan struct{}
	suspendDeltasChan chan struct{}
	resumeDeltasChan  chan struct{}

	snapshotIdentitiesTicker *utils.Ticker
	snapshotTicker           *utils.Ticker
}

func NewEntitiesWatcher(
	logger *log.Logger,
	observer *Observer,
	client_ *client.Client,
) EntitiesWatcher {
	ew := &entitiesWatcher{
		logger: logger,

		client: client_,

		observer:       observer,
		watchers:       map[GroupVersionResourceKind]Watcher{},
		watchersByKind: map[string]Watcher{},

		deltasQueue:       make(chan proto.PacketEntityDelta, deltasBufferChanSize),
		flushDeltasChan:   make(chan struct{}, 0),
		suspendDeltasChan: make(chan struct{}, 0),
		resumeDeltasChan:  make(chan struct{}, 0),
	}
	ew.snapshotIdentitiesTicker = utils.NewTicker("snapshot-identities", time.Minute, ew.snapshotIdentities)
	ew.snapshotTicker = utils.NewTicker("snapshot", 5*time.Minute, ew.snapshot)
	return ew
}

func (ew *entitiesWatcher) Start() {
	// this method should be called only once

	// TODO: if a packet expires or failed to be sent
	// we need to force a full resync to get all new updates

	for _, gvrk := range watchedResources {
		w := ew.observer.Watch(gvrk)
		ew.watchers[gvrk] = w
		ew.watchersByKind[gvrk.Kind] = w
	}

	ew.observer.WaitForCacheSync()

	ew.snapshotIdentitiesTicker.Start(true, false, false)
	ew.snapshotTicker.Start(true, false, false)

	for _, watcher := range ew.watchers {
		watcher.AddEventHandler(ew)
	}
	go ew.deltasWorker()
}

func (ew *entitiesWatcher) snapshotIdentities(tickTime time.Time) {
	//ew.suspendedDeltasWorker()
	//ew.flushDeltas()
	//defer ew.resumeDeltasWorker()

	packet := proto.PacketEntitiesResyncRequest{
		Snapshot:  map[string]proto.PacketEntitiesResyncItem{},
		Timestamp: tickTime.UTC(),
	}

	for gvrk, w := range ew.watchers {
		// no need for concurrent goroutines here because the lister uses
		// in-memory cashed data

		resource := gvrk.Resource

		ret, err := w.Lister().List(labels.Everything())
		if err != nil {
			ew.logger.Errorf(
				err,
				"unable to list %s", resource,
			)
		}
		if len(ret) == 0 {
			continue
		}

		var items = make([]*unstructured.Unstructured, len(ret))
		for i := range ret {
			u := *ret[i].(*unstructured.Unstructured)
			meta, found, err := unstructured.NestedFieldNoCopy(u.Object, "metadata")
			if !found || err != nil {
				ew.logger.Errorf(
					err,
					"unable to find metadata field of Unstructured: %s",
					resource,
				)
			}

			items[i] = &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind":       u.GetKind(),
					"apiVersion": u.GetAPIVersion(),
					"metadata":   meta,
				},
			}
		}
		packet.Snapshot[resource] = proto.PacketEntitiesResyncItem{
			Gvrk: packetGvrk(gvrk),
			Data: items,
		}
	}

	/*
		1. 	each 1h, send a full snapshot packet containing only identificators (name,kind, api-version)
			this packet will be used to only delete deleted entities.
			before sending this packet we must flush the deltas queue and be sure they are sent
		   	we don't need to suspend the deltas-worker because we don't use the snapshot to Upsert the data, only delete the deleted entities


		2. 	each 10m, resend all entities, as Upsert delta - that guarantees the data integrity even if we lost one event

	*/

	ew.client.Pipe(client.Package{
		Kind:        proto.PacketKindEntitiesResyncRequest,
		ExpiryTime:  utils.After(resyncPacketExpireAfter),
		ExpiryCount: resyncPacketExpireCount,
		Priority:    resyncPacketPriority,
		Retries:     resyncPacketRetries,
		Data:        packet,
	})
}

func (ew *entitiesWatcher) snapshot(tickTime time.Time) {
	// we don't need to stop nor suspend the deltas worker here
	// we actually are sending the snapshot using it.

	// send nodes and namespaces before all other deltas because they act as
	// parents for other resources
	nodesWatcher := ew.watchers[Nodes]
	ew.publishGvrk(Nodes, nodesWatcher, tickTime)

	namespacesWatcher := ew.watchers[Namespaces]
	ew.publishGvrk(Namespaces, namespacesWatcher, tickTime)

	for gvrk, w := range ew.watchers {
		// no need for concurrent goroutines here because the lister uses
		// in-memory cashed data

		if gvrk == Nodes || gvrk == Namespaces {
			// already sent
			continue
		}

		ew.publishGvrk(gvrk, w, tickTime)
	}
}

func (ew *entitiesWatcher) publishGvrk(
	gvrk GroupVersionResourceKind,
	w Watcher,
	tickTime time.Time,
) {
	resource := gvrk.Resource
	ret, err := w.Lister().List(labels.Everything())
	if err != nil {
		ew.logger.Errorf(
			err,
			"unable to list %s", resource,
		)
	}
	if len(ret) == 0 {
		return
	}
	for i := range ret {
		u := *ret[i].(*unstructured.Unstructured)
		ew.OnAdd(tickTime, gvrk, u)
	}
}

func (ew *entitiesWatcher) getParents(
	u *unstructured.Unstructured,
) (*proto.ParentController, error) {
	owners := u.GetOwnerReferences()
	var parent *proto.ParentController
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			parent = &proto.ParentController{
				Kind:       owner.Kind,
				Name:       owner.Name,
				APIVersion: owner.APIVersion,
			}

			watcher, ok := ew.watchersByKind[owner.Kind]
			if !ok {
				// not watched parent
				break
			}
			ownerObj, err := watcher.Lister().
				ByNamespace(u.GetNamespace()).
				Get(owner.Name)
			if err != nil {
				return nil, karma.Format(
					err,
					"unable to get parent owner",
				)
			}
			ownerU, ok := ownerObj.(*unstructured.Unstructured)
			if !ok {
				return nil, karma.Format(
					nil,
					"unable to cast runtime.Object to *unstructured.Unstructured",
				)
			}
			parentParent, err := ew.getParents(ownerU)
			if err != nil {
				return nil, karma.Format(
					err,
					"unable to get parent.parent",
				)
			}
			parent.Parent = parentParent
		}
	}
	return parent, nil
}

func (ew *entitiesWatcher) deltaWrapper(
	gvrk GroupVersionResourceKind,
	delta proto.PacketEntityDelta,
) (proto.PacketEntityDelta, error) {
	delta.Gvrk = packetGvrk(gvrk)

	if gvrk == Pods {
		parents, err := ew.getParents(&delta.Data)
		if err != nil {
			return delta, karma.Format(
				err,
				"unable to get pod parents",
			)
		}
		delta.Parents = parents
	}

	return delta, nil
}

func (ew *entitiesWatcher) OnAdd(
	now time.Time,
	gvrk GroupVersionResourceKind,
	obj unstructured.Unstructured,
) {
	delta, err := ew.deltaWrapper(
		gvrk,
		proto.PacketEntityDelta{
			DeltaKind: proto.EntityEventTypeUpsert,
			Data:      obj,
			Timestamp: now,
		},
	)
	if err != nil {
		ew.logger.Warningf(err, "unable to handle OnAdd delta")
		return
	}
	ew.deltasQueue <- delta
}

func (ew *entitiesWatcher) OnUpdate(
	now time.Time,
	gvrk GroupVersionResourceKind,
	oldObj, newObj unstructured.Unstructured,
) {
	delta, err := ew.deltaWrapper(
		gvrk,
		proto.PacketEntityDelta{
			DeltaKind: proto.EntityEventTypeUpsert,
			Data:      newObj,
			Timestamp: now,
		},
	)
	if err != nil {
		ew.logger.Warningf(err, "unable to handle onUpdate delta")
		return
	}
	ew.deltasQueue <- delta
}

func (ew *entitiesWatcher) OnDelete(
	now time.Time,
	gvrk GroupVersionResourceKind,
	obj unstructured.Unstructured,
) {
	delta, err := ew.deltaWrapper(
		gvrk,
		proto.PacketEntityDelta{
			DeltaKind: proto.EntityEventTypeDelete,
			Data:      obj,
			Timestamp: now,
		},
	)
	if err != nil {
		ew.logger.Warningf(err, "unable to handle OnDelete delta")
		return
	}
	ew.deltasQueue <- delta
}

func (ew *entitiesWatcher) flushDeltas() {
	ew.flushDeltasChan <- struct{}{}
}
func (ew *entitiesWatcher) suspendedDeltasWorker() {
	ew.suspendDeltasChan <- struct{}{}
}
func (ew *entitiesWatcher) resumeDeltasWorker() {
	ew.resumeDeltasChan <- struct{}{}
}
func (ew *entitiesWatcher) deltasWorker() {
	// this worker should be started only once

	for {
		items := map[string]proto.PacketEntityDelta{}
		t := time.Now()
		shouldFlush := false
		shouldSuspend := false
		waitForResume := func() {
			for {
				select {
				case <-ew.flushDeltasChan:
					ew.sendDeltas(items)
				case <-ew.resumeDeltasChan:
					return
				}
			}
		}
		for {
			select {
			case item := <-ew.deltasQueue:
				identifier := fmt.Sprintf(
					"%s:%s:%s",
					item.Data.GetNamespace(),
					item.Data.GetKind(),
					item.Data.GetName(),
				)
				oldItem, ok := items[identifier]
				if !ok {
					items[identifier] = item
				} else {
					if item.Timestamp.After(oldItem.Timestamp) {
						items[identifier] = item
					}
				}
				if len(items) >= deltasPacketFlushAfterSize ||
					time.Now().Sub(t) >= deltasPacketFlushAfterTime {
					shouldFlush = true
				}
			case <-ew.flushDeltasChan:
				shouldFlush = true
			case <-ew.suspendDeltasChan:
				shouldSuspend = true
			case <-time.After(deltasPacketFlushAfterTime):
				shouldFlush = true
			}
			if shouldFlush {
				ew.sendDeltas(items)
				break
			}
			if shouldSuspend {
				waitForResume()
			}
		}

		if shouldSuspend {
			waitForResume()
		}
	}
}

func (ew *entitiesWatcher) sendDeltas(deltas map[string]proto.PacketEntityDelta) {
	if len(deltas) == 0 {
		return
	}
	items := make([]proto.PacketEntityDelta, len(deltas))
	i := 0
	for _, item := range deltas {
		items[i] = item
		i++
	}
	packet := proto.PacketEntitiesDeltasRequest{
		Items:     items,
		Timestamp: time.Now().UTC(),
	}
	ew.client.Pipe(client.Package{
		Kind:        proto.PacketKindEntitiesDeltasRequest,
		ExpiryTime:  utils.After(deltasPacketExpireAfter),
		ExpiryCount: deltasPacketExpireCount,
		Priority:    deltasPacketPriority,
		Retries:     deltasPacketRetries,
		Data:        packet,
	})
}

func packetGvrk(gvrk GroupVersionResourceKind) proto.GroupVersionResourceKind {
	return proto.GroupVersionResourceKind{
		GroupVersionResource: gvrk.GroupVersionResource,
		Kind:                 gvrk.Kind,
	}
}
