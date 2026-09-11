// shard.go implements a single concurrency-safe bucket used by the sharded
// cache. Each shard owns its own RWMutex so independent keys rarely contend.
package cache

import (
	"sync"
)

// shard is one lock-protected map bucket of the sharded cache.
type shard[K comparable, V any] struct {
	hashmap map[K]V
	lock    sync.RWMutex
}

// set stores k→v under the shard's write lock.
func (s *shard[K, V]) set(k K, v V) {
	s.lock.Lock()
	s.hashmap[k] = v
	s.lock.Unlock()
}

// get loads the value for k; ok is false when the key is absent.
func (s *shard[K, V]) get(k K) (V, bool) {
	s.lock.RLock()
	item, exist := s.hashmap[k]
	s.lock.RUnlock()
	if !exist {
		var zero V
		return zero, false
	}
	return item, true
}

// del removes k and returns 1 if it existed, 0 otherwise.
func (s *shard[K, V]) del(k K) int {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, found := s.hashmap[k]; found {
		delete(s.hashmap, k)
		return 1
	}
	return 0
}

// clear empties the shard under the write lock.
func (s *shard[K, V]) clear() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.hashmap = map[K]V{}
}

// snapshot returns a copy of all key/value pairs in the shard.
func (s *shard[K, V]) snapshot() map[K]V {
	s.lock.RLock()
	defer s.lock.RUnlock()
	result := make(map[K]V, len(s.hashmap))
	for k, v := range s.hashmap {
		result[k] = v
	}
	return result
}

// size returns the number of entries in the shard.
func (s *shard[K, V]) size() int {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return len(s.hashmap)
}
