package client

import (
	"container/heap"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
)

// Package structure used to send packages over the channel
type Package struct {
	// Kind packet kind
	Kind proto.PacketKind
	// ExpiryTime time afterwards will not try to send packets
	// nil means never expires
	ExpiryTime *time.Time
	// ExpiryCount will expire if this number of packets come afterwards
	// 0 means never
	ExpiryCount int
	// Priority a number to indicate the order to send pending packets
	// the lower the value the more urget it is
	Priority int
	// Retries max number of retries before decreasing priority by one
	// 0 means never
	Retries int
	// retries actual number of reties
	retries int
	// index used internally to manage priority queue
	index int
	// time used internally to manage priority queue
	time time.Time
	// Data data to be sent
	Data interface{}
}

// PipeStore store interface for packageges
type PipeStore interface {
	// Add adds a package to be sent, returns the number of dropped packages since the last add
	Add(*Package) int
	// Peek gets the first available package
	// returns the same if called multiple times without ack unless a package expires
	// returns nil if nothing in the queue
	Peek() *Package
	// Acks that a peeked package has been sent, does nothing if the package has expired
	Ack(*Package)
	// Pop is an atomic peek and ack
	// returns nil in case there are no packages
	Pop() *Package
	// Len gets the number of pending packets
	Len() int
}

type DefaultPipeStore struct {
	sync.Mutex

	// items removed since last add
	removed int

	// items sorted by priority then time
	pq *PriorityQueue
	// to keep track of counts
	kinds map[proto.PacketKind][]*Package
}

func (s *DefaultPipeStore) Add(pack *Package) int {
	if pack == nil {
		panic("programming error, make sure you don't pass nil package")
	}
	s.Lock()
	defer s.Unlock()
	if (pack.time == time.Time{}) {
		pack.time = time.Now()
	}
	heap.Push(s.pq, pack)
	heap.Fix(s.pq, pack.index)

	// expire count
	kind, ok := s.kinds[pack.Kind]
	if !ok {
		kind = []*Package{}
		s.kinds[pack.Kind] = kind
	}
	kind = append(kind, pack)
	s.kinds[pack.Kind] = kind

	now := time.Now()
	i := 0
	for i < len(kind) {
		if (kind[i].ExpiryCount > 0 && kind[i].ExpiryCount < len(kind)) ||
			(kind[i].ExpiryTime != nil && now.After(*kind[i].ExpiryTime)) {
			s.removed++
			s.removeKind(kind[i], i)
			kind = s.kinds[pack.Kind]
		} else {
			i++
		}
	}

	removed := s.removed
	s.removed = 0
	return removed
}

func (s *DefaultPipeStore) Pop() *Package {
	s.Lock()
	defer s.Unlock()
	pack := s.peek()
	if pack != nil {
		s.ack(pack)
	}
	return pack
}

func (s *DefaultPipeStore) Peek() *Package {
	s.Lock()
	defer s.Unlock()
	return s.peek()
}

func (s *DefaultPipeStore) peek() *Package {
	var pack *Package

	for s.pq.Len() > 0 {
		// we are using first to make sure peek doesn't remove items from the pq
		pack = s.pq.First().(*Package)

		// check expiry time
		if pack.ExpiryTime != nil && time.Now().After(*pack.ExpiryTime) {
			s.removed++
			s.remove(pack)
		}
		break
	}
	if pack == nil {
		return nil
	}

	// decrease priority if number of retries increased
	pack.retries++
	if pack.Retries > 0 && pack.retries%pack.Retries == 0 {
		pack.Priority++
		heap.Fix(s.pq, pack.index)
	}
	return pack
}

func (s *DefaultPipeStore) Ack(pack *Package) {
	s.Lock()
	defer s.Unlock()
	s.ack(pack)
}

func (s *DefaultPipeStore) ack(pack *Package) {
	if pack.index >= 0 {
		s.remove(pack)
	}
}

func (s *DefaultPipeStore) remove(pack *Package) {
	heap.Remove(s.pq, pack.index)
	kind, ok := s.kinds[pack.Kind]
	if ok {
		// the loop will be executed only once most of the time
		// as the higher priority item for the same kind will mostly be the oldest one
		for i, p := range kind {
			if p == pack {
				kind = append(kind[:i], kind[i+1:]...)
				s.kinds[pack.Kind] = kind
				break
			}
		}
	}
}

func (s *DefaultPipeStore) removeKind(pack *Package, index int) {
	heap.Remove(s.pq, pack.index)
	kind, ok := s.kinds[pack.Kind]
	if ok {
		kind = append(kind[:index], kind[index+1:]...)
		s.kinds[pack.Kind] = kind
	}
}

func (s *DefaultPipeStore) Len() int {
	s.Lock()
	defer s.Unlock()
	return s.pq.Len()
}

func NewDefaultPipeStore() *DefaultPipeStore {
	pq := PriorityQueue{}
	heap.Init(&pq)
	return &DefaultPipeStore{
		pq:    &pq,
		kinds: map[proto.PacketKind][]*Package{},
	}
}

// PriorityQueue implements heap.Interface and holds Items.
type PriorityQueue []*Package

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	// We want Pop to give us the lowest, not highest, priority so we use less than here.
	return pq[i].Priority < pq[j].Priority || (pq[i].Priority == pq[j].Priority && pq[i].time.Before(pq[j].time))
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*Package)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) First() interface{} {
	return (*pq)[0]
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}
