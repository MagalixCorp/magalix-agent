package entities

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"

	corev1 "k8s.io/api/core/v1"
)

const (
	resyncInterval   = 4 * time.Hour
	snapshotInterval = 3 * time.Hour

	deltasBufferChanSize       = 1024
	deltasPacketFlushAfterSize = 100
	deltasPacketFlushAfterTime = time.Second * 10
)

var (
	watchedResources = []kuber.GroupVersionResourceKind{
		kuber.Nodes,
		kuber.Namespaces,
		kuber.LimitRanges,
		kuber.Pods,
		kuber.ReplicationControllers,
		kuber.Deployments,
		kuber.StatefulSets,
		kuber.DaemonSets,
		kuber.ReplicaSets,
		kuber.Jobs,
		kuber.CronJobs,
		kuber.Ingresses,
		kuber.NetworkPolicies,
		kuber.Services,
		kuber.PersistentVolumes,
		kuber.PersistentVolumeClaims,
		kuber.StorageClasses,
		kuber.Roles,
		kuber.RoleBindings,
		kuber.ClusterRoles,
		kuber.ClusterRoleBindings,
		kuber.ServiceAccounts,
	}
)

type EntitiesWatcher struct {
	observer *kuber.Observer

	watchers       map[kuber.GroupVersionResourceKind]kuber.Watcher
	watchersByKind map[string]kuber.Watcher
	deltasQueue    chan agent.Delta

	sendDeltas         agent.DeltasHandler
	sendEntitiesResync agent.EntitiesResyncHandler

	cancelWorker context.CancelFunc
}

func NewEntitiesWatcher(
	observer_ *kuber.Observer,
	version int,
) *EntitiesWatcher {
	if version >= 18 {
		watchedResources = append(watchedResources, kuber.IngressClasses)
	}

	ew := &EntitiesWatcher{
		observer:       observer_,
		watchers:       map[kuber.GroupVersionResourceKind]kuber.Watcher{},
		watchersByKind: map[string]kuber.Watcher{},

		deltasQueue: make(chan agent.Delta, deltasBufferChanSize),
	}
	return ew
}

func (ew *EntitiesWatcher) SetDeltasHandler(handler agent.DeltasHandler) {
	ew.sendDeltas = handler
}

func (ew *EntitiesWatcher) SetEntitiesResyncHandler(handler agent.EntitiesResyncHandler) {
	ew.sendEntitiesResync = handler
}

func (ew *EntitiesWatcher) Start(ctx context.Context) error {
	// this method should be called only once

	// TODO: if a packet expires or failed to be sent
	// we need to force a full resync to get all new updates

	for _, gvrk := range watchedResources {
		w := ew.observer.Watch(gvrk)
		ew.watchers[gvrk] = w
		ew.watchersByKind[gvrk.Kind] = w
	}

	// missing permissions cause a timeout, we ignore it so the agent is not blocked when permissions are missing
	err := ew.observer.WaitForCacheSync()
	if err != nil {
		logger.Warnf("timeout due to missing permissions with error %s ", err.Error())
	}

	for _, watcher := range ew.watchers {
		watcher.AddEventHandler(ew)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	ew.cancelWorker = cancel
	eg, egCtx := errgroup.WithContext(cancelCtx)
	eg.Go(func() error {
		ew.deltasWorker(egCtx)
		return nil
	})
	eg.Go(func() error {
		ew.snapshotWorker(egCtx)
		return nil
	})
	return eg.Wait()
}

func (ew *EntitiesWatcher) Stop() error {
	if ew.cancelWorker == nil {
		return nil
	}
	ew.cancelWorker()
	ew.cancelWorker = nil
	return nil
}

func (ew *EntitiesWatcher) WatcherFor(
	gvrk kuber.GroupVersionResourceKind,
) (kuber.Watcher, error) {
	w, ok := ew.watchers[gvrk]
	if !ok {
		return nil, fmt.Errorf(
			"non watched resource, gvrk: %+v",
			gvrk,
		)
	}
	return w, nil
}

func (ew *EntitiesWatcher) buildAndSendSnapshotResync() {
	resync := agent.EntitiesResync{
		Snapshot:  map[string]agent.EntitiesResyncItem{},
		Timestamp: time.Now(),
	}

	for gvrk, w := range ew.watchers {
		// no need for concurrent goroutines here because the lister uses
		// in-memory cached data

		resource := gvrk.Resource

		ret, err := w.Lister().List(labels.Everything())
		if err != nil {
			logger.Errorw(
				"unable to list "+resource,
				"error", err,
			)
		}
		if len(ret) == 0 {
			continue
		}

		var items = make([]*unstructured.Unstructured, len(ret))
		for i := range ret {
			u := *ret[i].(*unstructured.Unstructured)
			meta, err := getObjectMeta(&u)
			if err != nil {
				logger.Errorw(
					"unable to find metadata field",
					"error", err,
					"resource", resource,
				)
			}

			status, err := getObjectStatus(&u, gvrk)
			if err != nil {
				logger.Errorw("unable to get object status data", "error", err)
			}

			// TODO: Replace with basic identification data. The rest of the data shouldn't be needed
			items[i] = &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind":       u.GetKind(),
					"apiVersion": u.GetAPIVersion(),
					"metadata":   meta,
					"status":     status,
				},
			}
		}
		resync.Snapshot[resource] = agent.EntitiesResyncItem{
			Gvrk: packetGvrk(gvrk),
			Data: items,
		}
	}

	err := ew.sendEntitiesResync(&resync)
	if err != nil {
		logger.Errorf("Failed to send Entities Resync. %w", err)
	}
}

func (ew *EntitiesWatcher) sendSnapshot() {
	// send nodes and namespaces before all other deltas because they act as
	// parents for other resources
	nodesWatcher := ew.watchers[kuber.Nodes]
	ew.publishGvrk(kuber.Nodes, nodesWatcher)

	namespacesWatcher := ew.watchers[kuber.Namespaces]
	ew.publishGvrk(kuber.Namespaces, namespacesWatcher)

	for gvrk, w := range ew.watchers {
		// no need for concurrent goroutines here because the lister uses
		// in-memory cashed data

		if gvrk == kuber.Nodes || gvrk == kuber.Namespaces {
			// already sent
			continue
		}

		ew.publishGvrk(gvrk, w)
	}
}

func (ew *EntitiesWatcher) publishGvrk(
	gvrk kuber.GroupVersionResourceKind,
	w kuber.Watcher,
) {
	resource := gvrk.Resource
	ret, err := w.Lister().List(labels.Everything())
	if err != nil {
		logger.Errorf("unable to list %s. %w", resource, err)
	}
	if len(ret) == 0 {
		return
	}
	for i := range ret {
		u := *ret[i].(*unstructured.Unstructured)
		ew.OnAdd(gvrk, u)
	}
}

func (ew *EntitiesWatcher) getParents(
	u *unstructured.Unstructured,
) (*agent.ParentController, error) {
	parent, err := kuber.GetParents(
		u,
		ew.observer.ParentsStore,
		func(kind string) (watcher kuber.Watcher, b bool) {
			watcher, ok := ew.watchersByKind[kind]
			return watcher, ok
		},
	)
	if err != nil {
		return nil, err
	}

	return packetParent(parent), nil
}

func (ew *EntitiesWatcher) deltaWrapper(
	gvrk kuber.GroupVersionResourceKind,
	delta agent.Delta,
) (agent.Delta, error) {
	delta.Gvrk = packetGvrk(gvrk)

	if gvrk == kuber.Pods {
		parents, err := ew.getParents(&delta.Data)
		if err != nil {
			return delta, fmt.Errorf(
				"unable to get pod parents, error: %w",
				err,
			)
		}
		delta.Parent = parents
	}

	return delta, nil
}

func (ew *EntitiesWatcher) OnAdd(
	gvrk kuber.GroupVersionResourceKind,
	obj unstructured.Unstructured,
) {
	delta, err := ew.deltaWrapper(
		gvrk,
		agent.Delta{
			Kind:      agent.EntityDeltaKindUpsert,
			Data:      obj,
			Timestamp: time.Now(),
		},
	)
	if err != nil {
		logger.Warnw("unable to handle OnAdd delta", "error", err)
		return
	}
	ew.deltasQueue <- delta
}

func (ew *EntitiesWatcher) OnUpdate(
	gvrk kuber.GroupVersionResourceKind,
	oldObj, newObj unstructured.Unstructured,
) {
	delta, err := ew.deltaWrapper(
		gvrk,
		agent.Delta{
			Kind:      agent.EntityDeltaKindUpsert,
			Data:      newObj,
			Timestamp: time.Now(),
		},
	)
	if err != nil {
		logger.Warnw("unable to handle onUpdate delta", "error", err)
		return
	}
	ew.deltasQueue <- delta
}

func (ew *EntitiesWatcher) OnDelete(
	gvrk kuber.GroupVersionResourceKind,
	obj unstructured.Unstructured,
) {
	ew.observer.ParentsStore.Delete(obj.GetNamespace(), obj.GetKind(), obj.GetName())
	delta, err := ew.deltaWrapper(
		gvrk,
		agent.Delta{
			Kind:      agent.EntityDeltaKindDelete,
			Data:      obj,
			Timestamp: time.Now(),
		},
	)
	if err != nil {
		logger.Warnw("unable to handle OnDelete delta", "error", err)
		return
	}
	ew.deltasQueue <- delta
}

func (ew *EntitiesWatcher) deltasWorker(ctx context.Context) {
	// this worker should be started only once

	logger.Debug("Entities Watcher deltas worker started")

	// TODO: Review this logic
	for {
		items := map[string]agent.Delta{}
		t := time.Now()
		shouldFlush := false
		shouldStop := false
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
					time.Since(t) >= deltasPacketFlushAfterTime {
					shouldFlush = true
				}
			case <-time.After(deltasPacketFlushAfterTime):
				shouldFlush = true
			case <-ctx.Done():
				shouldFlush = true
				shouldStop = true
			}
			if shouldFlush {
				deltas := make([]*agent.Delta, 0, len(items))
				for _, item := range items {
					_item := item
					deltas = append(deltas, &_item)
				}
				// TODO: If an error happens while sending deltas, the items map should be preserved until sending succeeds
				err := ew.sendDeltas(deltas)
				if err != nil {
					logger.Errorf("Failed to send %d deltas. %w", len(deltas), err)
				}
				break
			}
		}
		if shouldStop {
			logger.Debug("Entities watcher deltas worker stopped")
			return
		}
	}
}

func (ew *EntitiesWatcher) snapshotWorker(ctx context.Context) {
	snapshotTicker := time.NewTicker(snapshotInterval)
	resyncTicker := time.NewTicker(resyncInterval)

	logger.Debug("Entities Watcher snapshot worker started")
	// Send snapshot & resync immediately once
	ew.sendSnapshot()
	ew.buildAndSendSnapshotResync()
	for {
		select {
		case <-ctx.Done():
			snapshotTicker.Stop()
			resyncTicker.Stop()
			logger.Debug("Entities Watcher snapshot worker stopped")
			return
		case <-snapshotTicker.C:
			ew.sendSnapshot()
		case <-resyncTicker.C:
			ew.buildAndSendSnapshotResync()
		}
	}
}

func packetGvrk(gvrk kuber.GroupVersionResourceKind) agent.GroupVersionResourceKind {
	return agent.GroupVersionResourceKind{
		GroupVersionResource: gvrk.GroupVersionResource,
		Kind:                 gvrk.Kind,
	}
}

func packetParent(parent *kuber.ParentController) *agent.ParentController {
	if parent == nil {
		return nil
	}
	return &agent.ParentController{
		Kind:       parent.Kind,
		Name:       parent.Name,
		APIVersion: parent.APIVersion,
		IsWatched:  parent.IsWatched,
		Parent:     packetParent(parent.Parent),
	}
}

func getObjectStatus(obj *unstructured.Unstructured, gvrk kuber.GroupVersionResourceKind) (map[string]interface{}, error) {
	switch gvrk {
	case kuber.Nodes:
		ip, found, err := getNodeInternalIP(obj)
		if !found || err != nil {
			return nil, fmt.Errorf("unable to find node internal ip, error: %w", err)
		}

		return map[string]interface{}{
			"addresses": []interface{}{
				map[string]interface{}{
					"type":    "InternalIP",
					"address": ip,
				},
			}}, nil
	default:
		return nil, nil
	}
}

func getObjectMeta(u *unstructured.Unstructured) (map[string]interface{}, error) {
	meta, found, err := unstructured.NestedFieldNoCopy(u.Object, "metadata")
	if !found || err != nil {
		return nil, fmt.Errorf("unable to obtain metadata, error: %w", err)
	}
	metadataMap, ok := meta.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("metadata is type %T, map[string]interface{} expected", meta)
	}
	ownerRef, ok := metadataMap["ownerReferences"].([]interface{})
	hasOwner := false
	if ok && len(ownerRef) > 0 {
		hasOwner = true
	}
	return map[string]interface{}{
		"namespace": metadataMap["namespace"],
		"name":      metadataMap["name"],
		"hasOwner":  hasOwner,
	}, nil
}

func getNodeInternalIP(node *unstructured.Unstructured) (string, bool, error) {
	addresses, found, err := unstructured.NestedSlice(node.Object, "status", "addresses")
	if !found || err != nil {
		return "", found, err
	}

	for _, address := range addresses {
		addressMap, ok := address.(map[string]interface{})
		if !ok {
			return "", false, fmt.Errorf("%v of type %T is not map[string]interface{}", address, address)
		}
		addressType, ok := addressMap["type"].(string)
		if !ok {
			return "", false, fmt.Errorf("%v of type %T is not string", addressMap["type"], addressMap["type"])
		}
		if addressType == string(corev1.NodeInternalIP) {
			internalIP, ok := addressMap["address"].(string)
			if !ok {
				return "", false, fmt.Errorf("%v of type %T is not string", addressMap["address"], addressMap["address"])
			}
			return internalIP, true, nil
		}
	}

	return "", false, nil
}
