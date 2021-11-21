package entities

import (
	"context"
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/kuber"
	"github.com/MagalixTechnologies/core/logger"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
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

type EntitiesWatcherSource interface {
	AddResourceEventsHandler(handler ResourceEventsHandler)
	GetAllEntitiesByGvrk() (map[kuber.GroupVersionResourceKind][]unstructured.Unstructured, []error)
	GetParents(namespace string, kind string, name string) (*kuber.ParentController, bool)
}

type ResourceEventsHandler interface {
	OnResourceAdd(gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured)
	OnResourceUpdate(gvrk kuber.GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured)
	OnResourceDelete(gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured)
	OnCacheSync()
}

type EntitiesWatcher struct {
	observer *kuber.Observer

	watchers              map[kuber.GroupVersionResourceKind]kuber.Watcher
	watchersByKind        map[string]kuber.Watcher
	deltasQueue           chan agent.Delta
	resourceEventHandlers map[ResourceEventsHandler]struct{}

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
		observer:              observer_,
		watchers:              map[kuber.GroupVersionResourceKind]kuber.Watcher{},
		watchersByKind:        map[string]kuber.Watcher{},
		deltasQueue:           make(chan agent.Delta, deltasBufferChanSize),
		resourceEventHandlers: make(map[ResourceEventsHandler]struct{}),
	}
	return ew
}

func (ew *EntitiesWatcher) AddResourceEventsHandler(handler ResourceEventsHandler) {
	ew.resourceEventHandlers[handler] = struct{}{}
}

func (ew *EntitiesWatcher) Start(ctx context.Context) error {
	// this method should be called only once

	// TODO: if a packet expires or failed to be sent
	// 	we need to force a full resync to get all new updates

	for _, gvrk := range watchedResources {
		w := ew.observer.Watch(gvrk)
		ew.watchers[gvrk] = w
		ew.watchersByKind[gvrk.Kind] = w
	}

	ew.WaitForCacheSync()

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

func (ew *EntitiesWatcher) WatcherFor(gvrk kuber.GroupVersionResourceKind) (kuber.Watcher, error) {
	w, ok := ew.watchers[gvrk]
	if !ok {
		return nil, fmt.Errorf(
			"non watched resource, gvrk: %+v",
			gvrk,
		)
	}
	return w, nil
}

func (ew *EntitiesWatcher) WaitForCacheSync() {
	// missing permissions cause a timeout, we ignore it so the agent is not blocked when permissions are missing
	err := ew.observer.WaitForCacheSync()
	if err != nil {
		logger.Warnf("timeout due to missing permissions with error %s ", err.Error())
	}
	for handler := range ew.resourceEventHandlers {
		handler.OnCacheSync()
	}
}

func (ew *EntitiesWatcher) GetAllEntitiesByGvrk() (map[kuber.GroupVersionResourceKind][]unstructured.Unstructured, []error) {
	entities := make(map[kuber.GroupVersionResourceKind][]unstructured.Unstructured)
	errs := make([]error, 0)
	for gvrk, w := range ew.watchers {
		resource := gvrk.Resource
		ret, err := w.Lister().List(labels.Everything())
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to list %s. %w", resource, err))
		}
		uList := make([]unstructured.Unstructured, 0, len(ret))
		for _, obj := range ret {
			u := *obj.(*unstructured.Unstructured)
			uList = append(uList, u)
		}
		entities[gvrk] = uList
	}
	return entities, errs
}

func (ew *EntitiesWatcher) getParents(u *unstructured.Unstructured) (*agent.ParentController, error) {
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

func (ew *EntitiesWatcher) deltaWrapper(gvrk kuber.GroupVersionResourceKind, delta agent.Delta) (agent.Delta, error) {
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

func (ew *EntitiesWatcher) OnAdd(gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured) {
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

	for handler := range ew.resourceEventHandlers {
		handler.OnResourceAdd(gvrk, obj)
	}
}

func (ew *EntitiesWatcher) OnUpdate(gvrk kuber.GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured) {
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

	for handler := range ew.resourceEventHandlers {
		handler.OnResourceUpdate(gvrk, oldObj, newObj)
	}
}

func (ew *EntitiesWatcher) OnDelete(gvrk kuber.GroupVersionResourceKind, obj unstructured.Unstructured) {
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

	for handler := range ew.resourceEventHandlers {
		handler.OnResourceDelete(gvrk, obj)
	}
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
				break
			}
		}
		if shouldStop {
			logger.Debug("Entities watcher deltas worker stopped")
			return
		}
	}
}

func (ew *EntitiesWatcher) GetParents(namespace string, kind string, name string) (*kuber.ParentController, bool) {
	return ew.observer.ParentsStore.GetParents(namespace, kind, name)
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
