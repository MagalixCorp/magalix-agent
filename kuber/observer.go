package kuber

import (
	"bytes"
	"time"

	"github.com/MagalixTechnologies/log-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type Observer struct {
	dynamicinformer.DynamicSharedInformerFactory

	logger *log.Logger
	stopCh chan struct{}
}

func NewObserver(
	logger *log.Logger,
	client dynamic.Interface,
	stopCh chan struct{},
	defaultResync time.Duration,
) *Observer {
	return &Observer{
		DynamicSharedInformerFactory: dynamicinformer.NewDynamicSharedInformerFactory(client, defaultResync),

		logger: logger,
		stopCh: stopCh,
	}
}

func (observer *Observer) Watch(
	gvrk GroupVersionResourceKind,
) *watcher {
	observer.logger.Infof(
		nil,
		"subscribed on changes about resource: %s",
		gvrk.String(),
	)

	watcher := observer.WatcherFor(gvrk)
	observer.Start()

	return watcher
}

func (observer *Observer) WatcherFor(
	gvrk GroupVersionResourceKind,
) *watcher {
	informer := observer.ForResource(gvrk.GroupVersionResource)

	return &watcher{
		gvrk:     gvrk,
		logger:   observer.logger,
		informer: informer,
	}
}

func (observer *Observer) Start() {
	observer.DynamicSharedInformerFactory.Start(observer.stopCh)
}

func (observer *Observer) Stop() {
	observer.stopCh <- struct{}{}
}

func (observer *Observer) WaitForCacheSync() {
	observer.DynamicSharedInformerFactory.WaitForCacheSync(observer.stopCh)
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
	logger   *log.Logger
	informer informers.GenericInformer
}

func (w *watcher) GetGroupVersionResourceKind() GroupVersionResourceKind {
	return w.gvrk
}

func (w *watcher) Lister() cache.GenericLister {
	return w.informer.Lister()
}

func (w *watcher) AddEventHandler(handler ResourceEventHandler) {
	w.informer.Informer().AddEventHandler(wrapHandler(handler, w.logger, w.gvrk))
}

func (w *watcher) AddEventHandlerWithResyncPeriod(handler ResourceEventHandler, resyncPeriod time.Duration) {
	w.informer.Informer().AddEventHandlerWithResyncPeriod(wrapHandler(handler, w.logger, w.gvrk), resyncPeriod)
}

func (w *watcher) HasSynced() bool {
	return w.informer.Informer().HasSynced()
}

func (w *watcher) LastSyncResourceVersion() string {
	return w.informer.Informer().LastSyncResourceVersion()
}

func wrapHandler(
	wrapped ResourceEventHandler,
	logger *log.Logger,
	gvrk GroupVersionResourceKind,
) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			now := time.Now().UTC()
			objUn, oldOk := obj.(*unstructured.Unstructured)
			if !oldOk {
				logger.Error("unable to cast obj to *Unstructured")
			}
			if objUn != nil {
				wrapped.OnAdd(now, gvrk, *objUn)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			now := time.Now().UTC()
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
					logger.Errorf(err, "unable to marshal oldUn to json")
				}
				newJson, err := newUn.MarshalJSON()
				if err != nil {
					logger.Errorf(err, "unable to marshal newUn to json")
				}
				if err == nil {
					if bytes.Equal(oldJson, newJson) {
						return
					}
				}
			}
			if oldUn != nil && newUn != nil {
				wrapped.OnUpdate(now, gvrk, *oldUn, *newUn)
			}
		},
		DeleteFunc: func(obj interface{}) {
			now := time.Now().UTC()
			objUn, oldOk := obj.(*unstructured.Unstructured)
			if !oldOk {
				logger.Error("unable to cast obj to *Unstructured")
			}
			if objUn != nil {
				wrapped.OnDelete(now, gvrk, *objUn)
			}
		},
	}
}

type ResourceEventHandler interface {
	OnAdd(now time.Time, gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
	OnUpdate(now time.Time, gvrk GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured)
	OnDelete(now time.Time, gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
}

// ResourceEventHandlerFuncs is an adaptor to let you easily specify as many or
// as few of the notification functions as you want while still implementing
// ResourceEventHandler.
type ResourceEventHandlerFuncs struct {
	AddFunc    func(now time.Time, gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
	UpdateFunc func(now time.Time, gvrk GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured)
	DeleteFunc func(now time.Time, gvrk GroupVersionResourceKind, obj unstructured.Unstructured)
}

// OnAdd calls AddFunc if it's not nil.
func (r ResourceEventHandlerFuncs) OnAdd(now time.Time, gvrk GroupVersionResourceKind, obj unstructured.Unstructured) {
	if r.AddFunc != nil {
		r.AddFunc(now, gvrk, obj)
	}
}

// OnUpdate calls UpdateFunc if it's not nil.
func (r ResourceEventHandlerFuncs) OnUpdate(now time.Time, gvrk GroupVersionResourceKind, oldObj, newObj unstructured.Unstructured) {
	if r.UpdateFunc != nil {
		r.UpdateFunc(now, gvrk, oldObj, newObj)
	}
}

// OnDelete calls DeleteFunc if it's not nil.
func (r ResourceEventHandlerFuncs) OnDelete(now time.Time, gvrk GroupVersionResourceKind, obj unstructured.Unstructured) {
	if r.DeleteFunc != nil {
		r.DeleteFunc(now, gvrk, obj)
	}
}
