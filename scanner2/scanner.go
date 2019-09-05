package scanner2

import (
	"fmt"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"time"

	"github.com/MagalixCorp/magalix-agent/client"
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
	watchedResources = []schema.GroupVersionResource{
		Nodes,
		Namespaces,
		LimitRanges,
		Pods,
		ReplicationControllers,
		Deployments,
		StatefulSets,
		DaemonSets,
		ReplicaSets,
		CronJobs,
	}
)

type EntitiesWatcher interface {
	Start()
}

type entitiesWatcher struct {
	logger   *log.Logger
	client   *client.Client
	observer *Observer

	watchers          map[schema.GroupVersionResource]Watcher
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
	// TODO: if a packet expires or failed to be sent
	// we need to force a full resync to get all new updates

	ew := &entitiesWatcher{
		logger: logger,

		client: client_,

		observer: observer,
		watchers: map[schema.GroupVersionResource]Watcher{},

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
	for _, gvr := range watchedResources {
		w := ew.observer.Watch(gvr)
		ew.watchers[gvr] = w
		w.AddEventHandler(ew)
	}
	go ew.deltasWorker()

	ew.observer.WaitForCacheSync()

	ew.snapshotIdentitiesTicker.Start(true, false, false)
	ew.snapshotTicker.Start(false, false, false)
}

func (ew *entitiesWatcher) snapshotIdentities(tickTime time.Time) {
	//ew.suspendedDeltasWorker()
	//ew.flushDeltas()
	//defer ew.resumeDeltasWorker()

	packet := proto.PacketEntitiesResyncRequest{
		Snapshot:  map[string]proto.PacketEntitiesResyncItem{},
		Timestamp: tickTime.UTC(),
	}

	for gvr, w := range ew.watchers {
		// no need for concurrent goroutines here because the lister uses
		// in-memory cashed data

		resource := gvr.Resource

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
			Gvr:  gvr,
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

	for gvr, w := range ew.watchers {
		// no need for concurrent goroutines here because the lister uses
		// in-memory cashed data

		resource := gvr.Resource

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

		for i := range ret {
			u := *ret[i].(*unstructured.Unstructured)
			ew.OnAdd(tickTime, gvr, u)
		}
	}
}

func (ew *entitiesWatcher) OnAdd(now time.Time, gvr schema.GroupVersionResource, obj unstructured.Unstructured) {
	ew.deltasQueue <- proto.PacketEntityDelta{
		Gvr:       gvr,
		DeltaKind: proto.EntityEventTypeUpsert,
		Data:      obj,
		Timestamp: now,
	}
}

func (ew *entitiesWatcher) OnUpdate(now time.Time, gvr schema.GroupVersionResource, oldObj, newObj unstructured.Unstructured) {
	ew.deltasQueue <- proto.PacketEntityDelta{
		Gvr:       gvr,
		DeltaKind: proto.EntityEventTypeUpsert,
		Data:      newObj,
		Timestamp: now,
	}
}

func (ew *entitiesWatcher) OnDelete(now time.Time, gvr schema.GroupVersionResource, obj unstructured.Unstructured) {
	ew.deltasQueue <- proto.PacketEntityDelta{
		Gvr:       gvr,
		DeltaKind: proto.EntityEventTypeDelete,
		Data:      obj,
		Timestamp: now,
	}
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
