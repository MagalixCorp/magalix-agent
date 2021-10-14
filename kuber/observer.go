package kuber

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/MagalixTechnologies/core/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type Observer struct {
	dynamicinformer.DynamicSharedInformerFactory
	ParentsStore *ParentsStore

	stopCh chan struct{}
}

func NewObserver(client dynamic.Interface, parentsStore *ParentsStore, stopCh chan struct{}, defaultResync time.Duration) *Observer {
	return &Observer{
		DynamicSharedInformerFactory: dynamicinformer.NewDynamicSharedInformerFactory(client, defaultResync),
		ParentsStore:                 parentsStore,
		stopCh:                       stopCh,
	}
}

func (observer *Observer) Watch(gvrk GroupVersionResourceKind) *watcher {
	logger.Debugw("subscribed on changes", "resource", gvrk.String())

	watcher := observer.WatcherFor(gvrk)
	observer.Start()

	return watcher
}

func (observer *Observer) WatchAndWaitForSync(gvrk GroupVersionResourceKind) (*watcher, error) {
	watcher := observer.Watch(gvrk)

	done := make(chan struct{}, 1)

	go func() {
		cache.WaitForCacheSync(observer.stopCh, watcher.informer.Informer().HasSynced)
		done <- struct{}{}
	}()

	timeout := 5 * time.Second
	select {
	case <-done:
		return watcher, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("Time out waiting for informer sync: (GVRK, %+v), (timeout, %v)", gvrk, timeout)
	}
}

func (observer *Observer) WatcherFor(gvrk GroupVersionResourceKind) *watcher {
	informer := observer.ForResource(gvrk.GroupVersionResource)

	return &watcher{
		gvrk:     gvrk,
		informer: informer,
	}
}

func (observer *Observer) Start() {
	observer.DynamicSharedInformerFactory.Start(observer.stopCh)
}

func (observer *Observer) Stop() {
	observer.stopCh <- struct{}{}
}

func (observer *Observer) WaitForCacheSync() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	finished := make(chan struct{})

	go func() {
		observer.DynamicSharedInformerFactory.WaitForCacheSync(observer.stopCh)
		finished <- struct{}{}
	}()

	for {
		select {
		case <-finished:
			return nil
		case <-ctx.Done():
			return fmt.Errorf(
				"timeout waiting for cache sync",
			)
		}
	}

}

type Watcher interface {
	GetGroupVersionResourceKind() GroupVersionResourceKind

	Lister() cache.GenericLister

	// AddEventHandler adds an event handler to the shared informer using the shared informer's resync
	// period.  Events to a single handler are delivered sequentially, but there is no coordination
	// between different handlers.
	AddEventHandler(handler ResourceEventHandler)
	// AddEventHandlerWithResyncPeriod adds an event handler to the
	// shared informer using the specified resync period.  The resync
	// operation consists of delivering to the handler a create
	// notification for every object in the informer's local cache; it
	// does not add any interactions with the authoritative storage.
	AddEventHandlerWithResyncPeriod(handler ResourceEventHandler, resyncPeriod time.Duration)

	// HasSynced returns true if the shared informer's store has been
	// informed by at least one full LIST of the authoritative state
	// of the informer's object collection.  This is unrelated to "resync".
	HasSynced() bool
	// LastSyncResourceVersion is the resource version observed when last synced with the underlying
	// store. The value returned is not synchronized with access to the underlying store and is not
	// thread-safe.
	LastSyncResourceVersion() string
}

type watcher struct {
	gvrk     GroupVersionResourceKind
	informer informers.GenericInformer
}

func (w *watcher) GetGroupVersionResourceKind() GroupVersionResourceKind {
	return w.gvrk
}

func (w *watcher) Lister() cache.GenericLister {
	return w.informer.Lister()
}

func (w *watcher) AddEventHandler(handler ResourceEventHandler) {
	w.informer.Informer().AddEventHandler(wrapHandler(handler, w.gvrk))
}

func (w *watcher) AddEventHandlerWithResyncPeriod(handler ResourceEventHandler, resyncPeriod time.Duration) {
	w.informer.Informer().AddEventHandlerWithResyncPeriod(wrapHandler(handler, w.gvrk), resyncPeriod)
}

func (w *watcher) HasSynced() bool {
	return w.informer.Informer().HasSynced()
}

func (w *watcher) LastSyncResourceVersion() string {
	return w.informer.Informer().LastSyncResourceVersion()
}

func wrapHandler(wrapped ResourceEventHandler, gvrk GroupVersionResourceKind) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			objUn, ok := obj.(*unstructured.Unstructured)
			if !ok {
				logger.Error("unable to cast obj to *Unstructured")
			}
			if objUn != nil {
				objUn, err := maskUnstructured(objUn)
				if err != nil {
					logger.Errorw("unable to mask Unstructured", "error", err)
					return
				}
				wrapped.OnAdd(gvrk, *objUn)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// TODO: can we have a better way to suppress update events when
			// a resync is forced because of a network error
			oldUn, oldOk := oldObj.(*unstructured.Unstructured)
			if !oldOk {
				logger.Error("unable to cast oldObj to *Unstructured")
			}
			newUn, newOk := newObj.(*unstructured.Unstructured)
			if !newOk {
				logger.Error("unable to cast newObj to *Unstructured")
			}

			if oldOk && newOk &&
				oldUn.GetResourceVersion() == newUn.GetResourceVersion() {
				// deep check that nothing has changed
				oldJson, err := oldUn.MarshalJSON()
				if err != nil {
					logger.Errorw("unable to marshal oldUn to json", "error", err)
				}
				newJson, err := newUn.MarshalJSON()
				if err != nil {
					logger.Errorf("unable to marshal newUn to json", "error", err)
				}
				if err == nil {
					if bytes.Equal(oldJson, newJson) {
						return
					}
				}
			}
			if oldUn != nil && newUn != nil {
				oldUn, err := maskUnstructured(oldUn)
				if err != nil {
					logger.Errorw("unable to mask Unstructured", "error", err)
				}
				newUn, err := maskUnstructured(newUn)
				if err != nil {
					logger.Errorw("unable to mask Unstructured", "error", err)
					return
				}
				wrapped.OnUpdate(gvrk, *oldUn, *newUn)
			}
		},
		DeleteFunc: func(obj interface{}) {
			objUn, ok := obj.(*unstructured.Unstructured)
			if !ok {
				logger.Error("unable to cast obj to *Unstructured")
			}
			if objUn != nil {
				objUn, err := maskUnstructured(objUn)
				if err != nil {
					logger.Errorw("unable to mask Unstructured", "error", err)
					return
				}
				wrapped.OnDelete(gvrk, *objUn)
			}
		},
	}
}

type ResourceEventHandler interface {
	OnAdd(gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
	OnUpdate(gvrk GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured)
	OnDelete(gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
}

// ResourceEventHandlerFuncs is an adaptor to let you easily specify as many or
// as few of the notification functions as you want while still implementing
// ResourceEventHandler.
type ResourceEventHandlerFuncs struct {
	Observer   *Observer
	AddFunc    func(gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
	UpdateFunc func(gvrk GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured)
	DeleteFunc func(gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
}

// OnAdd calls AddFunc if it's not nil.
func (r ResourceEventHandlerFuncs) OnAdd(gvrk GroupVersionResourceKind, obj unstructured.Unstructured) {
	if r.AddFunc != nil {
		r.AddFunc(gvrk, obj)
	}
}

// OnUpdate calls UpdateFunc if it's not nil.
func (r ResourceEventHandlerFuncs) OnUpdate(gvrk GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured) {
	if r.UpdateFunc != nil {
		r.UpdateFunc(gvrk, oldObj, newObj)
	}
}

// OnDelete calls DeleteFunc if it's not nil.
func (r ResourceEventHandlerFuncs) OnDelete(gvrk GroupVersionResourceKind, obj unstructured.Unstructured) {
	if r.DeleteFunc != nil {
		r.DeleteFunc(gvrk, obj)
	}

	r.Observer.ParentsStore.Delete(obj.GetNamespace(), obj.GetKind(), obj.GetName())
}
