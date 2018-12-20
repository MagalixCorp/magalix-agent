package proc

import (
	"math"
	"sync"
	"sync/atomic"
)

// Syncer keeps track of synced entities
type Syncer struct {
	processed       map[string][]string
	locks           map[string]*sync.RWMutex
	versions        map[string]string
	synced          map[string]bool
	mutex           *sync.Mutex
	lockedResources *int64
	onSync          func()
	done            bool
}

// NewSyncer creates a new syncer
func NewSyncer() *Syncer {
	return &Syncer{
		processed: map[string][]string{},
		locks:     map[string]*sync.RWMutex{},
		versions:  map[string]string{},
		synced: map[string]bool{
			"replicationcontroller": false,
			"pod":                   false,
			"deployment":            false,
		},
		mutex:           &sync.Mutex{},
		lockedResources: new(int64),
		done:            false,
	}
}

func (syncer *Syncer) isDone() bool {
	syncer.mutex.Lock()
	defer syncer.mutex.Unlock()

	return syncer.done
}

// OnHandle look a given resource to start handling
func (syncer *Syncer) OnHandle(resource string, version string) {
	if syncer.isDone() {
		return
	}

	syncer.rlock(resource)
}

// OnProcess start processing resource and unlock
func (syncer *Syncer) OnProcess(resource string, version string) {
	if syncer.isDone() {
		return
	}

	syncer.addProcessed(resource, version)

	syncer.runlock(resource)

	syncer.syncResource(resource, version)
}

func (syncer *Syncer) syncResource(resource string, version string) {
	// corner case: there is no such resource in cluster
	if version != "1" {
		synced := syncer.isProcessed(resource)
		if !synced {
			return
		}
	}

	syncer.setSynced(resource)
	syncer.wlock(resource)
	defer syncer.wunlock(resource)

	syncer.sync()
}

func (syncer *Syncer) sync() {
	syncer.mutex.Lock()
	ready := true
	for resource, value := range syncer.synced {
		if !value {
			tracef(
				nil,
				"resource %s is not synced yet",
				resource,
			)

			ready = false
			break
		}
	}
	syncer.mutex.Unlock()

	if !ready {
		return
	}

	syncer.onSync()

	syncer.mutex.Lock()

	// TODO: remove

	if *syncer.lockedResources > 0 {
		logger.Errorf(nil, "possible leak, there are %d resources that are still locked after sync", syncer.lockedResources)
	}
	// for _, mutex := range syncer.locks {
	// 	mutex.Unlock()
	// }

	syncer.done = true

	syncer.mutex.Unlock()
}

func (syncer *Syncer) setSynced(resource string) {
	syncer.mutex.Lock()
	defer syncer.mutex.Unlock()

	tracef(nil, "resource %s synced", resource)

	syncer.synced[resource] = true
}

func (syncer *Syncer) getMutex(resource string) *sync.RWMutex {
	syncer.mutex.Lock()
	defer syncer.mutex.Unlock()

	mutex, ok := syncer.locks[resource]
	if !ok {
		mutex = &sync.RWMutex{}
		syncer.locks[resource] = mutex
	}

	return mutex
}

func (syncer *Syncer) wlock(resource string) {
	atomic.AddInt64(syncer.lockedResources, 1)
	syncer.getMutex(resource).Lock()
	atomic.AddInt64(syncer.lockedResources, -1)
}

func (syncer *Syncer) wunlock(resource string) {
	atomic.AddInt64(syncer.lockedResources, 1)
	syncer.getMutex(resource).Unlock()
	atomic.AddInt64(syncer.lockedResources, -1)
}

func (syncer *Syncer) rlock(resource string) {
	atomic.AddInt64(syncer.lockedResources, 1)
	syncer.getMutex(resource).RLock()
	atomic.AddInt64(syncer.lockedResources, -1)
}

func (syncer *Syncer) runlock(resource string) {
	atomic.AddInt64(syncer.lockedResources, 1)
	syncer.getMutex(resource).RUnlock()
	atomic.AddInt64(syncer.lockedResources, -1)
}

func (syncer *Syncer) addProcessed(resource string, version string) {
	syncer.mutex.Lock()
	defer syncer.mutex.Unlock()

	_, ok := syncer.processed[resource]
	if !ok {
		syncer.processed[resource] = []string{}
	}

	tracef(nil, "syncer: processed %s %s", resource, version)
	// cover the last 10 mins as watcher loop every 100 miliseconds  ->  see Observer.watch
	l := int(math.Max(float64(len(syncer.processed[resource])-6000), 0))
	syncer.processed[resource] = append(syncer.processed[resource][l:], version)
}

func (syncer *Syncer) isProcessed(resource string) bool {
	syncer.mutex.Lock()
	defer syncer.mutex.Unlock()

	informed, ok := syncer.versions[resource]
	if !ok {
		return false
	}

	found := false
	for _, processed := range syncer.processed[resource] {
		if processed == informed {
			found = true
			break
		}
	}

	return found
}

// InformResource set version and sync resource
func (syncer *Syncer) InformResource(resource string, version string) {
	syncer.setVersion(resource, version)

	if syncer.isDone() {
		return
	}
	syncer.syncResource(resource, version)
}

func (syncer *Syncer) setVersion(resource, version string) {
	syncer.mutex.Lock()
	defer syncer.mutex.Unlock()

	tracef(nil, "syncer: resource %s informed about %s", resource, version)

	syncer.versions[resource] = version
}

// SetOnSync set on sync hook
func (syncer *Syncer) SetOnSync(fn func()) {
	syncer.mutex.Lock()
	defer syncer.mutex.Unlock()

	syncer.onSync = fn
}
