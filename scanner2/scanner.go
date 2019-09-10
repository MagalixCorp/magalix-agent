package scanner2

import (
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/observer"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
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
	watchedResources = []observer.GroupVersionResourceKind{
		observer.Nodes,
		observer.Namespaces,
		observer.Pods,
		observer.ReplicationControllers,
		observer.Deployments,
		observer.StatefulSets,
		observer.DaemonSets,
		observer.ReplicaSets,
		observer.Jobs,
		observer.CronJobs,
	}
)

type EntitiesWatcher interface {
	Start()
}

type entitiesWatcher struct {
	logger   *log.Logger
	client   *client.Client
	observer *observer.Observer

	watchers       map[observer.GroupVersionResourceKind]observer.Watcher
	watchersByKind map[string]observer.Watcher
	deltasQueue    chan proto.PacketEntityDelta

	snapshotIdentitiesTicker *utils.Ticker
	snapshotTicker           *utils.Ticker
}

func NewEntitiesWatcher(
	logger *log.Logger,
	observer_ *observer.Observer,
	client_ *client.Client,
) EntitiesWatcher {
	ew := &entitiesWatcher{
		logger: logger,

		client: client_,

		observer:       observer_,
		watchers:       map[observer.GroupVersionResourceKind]observer.Watcher{},
		watchersByKind: map[string]observer.Watcher{},

		deltasQueue: make(chan proto.PacketEntityDelta, deltasBufferChanSize),
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
	// send nodes and namespaces before all other deltas because they act as
	// parents for other resources
	nodesWatcher := ew.watchers[observer.Nodes]
	ew.publishGvrk(observer.Nodes, nodesWatcher, tickTime)

	namespacesWatcher := ew.watchers[observer.Namespaces]
	ew.publishGvrk(observer.Namespaces, namespacesWatcher, tickTime)

	for gvrk, w := range ew.watchers {
		// no need for concurrent goroutines here because the lister uses
		// in-memory cashed data

		if gvrk == observer.Nodes || gvrk == observer.Namespaces {
			// already sent
			continue
		}

		ew.publishGvrk(gvrk, w, tickTime)
	}
}

func (ew *entitiesWatcher) publishGvrk(
	gvrk observer.GroupVersionResourceKind,
	w observer.Watcher,
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
	gvrk observer.GroupVersionResourceKind,
	delta proto.PacketEntityDelta,
) (proto.PacketEntityDelta, error) {
	delta.Gvrk = packetGvrk(gvrk)

	if gvrk == observer.Pods {
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
	gvrk observer.GroupVersionResourceKind,
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
	gvrk observer.GroupVersionResourceKind,
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
	gvrk observer.GroupVersionResourceKind,
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

func (ew *entitiesWatcher) deltasWorker() {
	// this worker should be started only once

	for {
		items := map[string]proto.PacketEntityDelta{}
		t := time.Now()
		shouldFlush := false
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
			case <-time.After(deltasPacketFlushAfterTime):
				shouldFlush = true
			}
			if shouldFlush {
				ew.sendDeltas(items)
				break
			}
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

func packetGvrk(gvrk observer.GroupVersionResourceKind) proto.GroupVersionResourceKind {
	return proto.GroupVersionResourceKind{
		GroupVersionResource: gvrk.GroupVersionResource,
		Kind:                 gvrk.Kind,
	}
}
