package proc

// Lockable Same interface as sync.Locker but sync
// package doesn't have interface for read locker
type Lockable interface {
	Lock()
	Unlock()
}

// ReadLockable defines only read lock functions
type ReadLockable interface {
	RLock()
	RUnlock()
}

// WithLock runs a function with a lock
func WithLock(lockable Lockable, fn func()) {
	lockable.Lock()
	defer lockable.Unlock()

	fn()
}

// WithReadLock runs a function with read lock
func WithReadLock(lockable ReadLockable, fn func()) {
	lockable.RLock()
	defer lockable.RUnlock()

	fn()
}
