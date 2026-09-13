// This implementation is based on and modified from https://github.com/fanjindong/go-cache
package cache

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/cespare/xxhash/v2"
)

// keyToString 把泛型 key 转为哈希输入字符串。整型 key 是渠道/模型/密钥缓存的主键,
// 走 strconv 避免 fmt 反射分配; 其余类型(如命名 string 类型)回退 fmt。
func keyToString[K comparable](key K) string {
	switch k := any(key).(type) {
	case string:
		return k
	case int:
		return strconv.Itoa(k)
	case int64:
		return strconv.FormatInt(k, 10)
	default:
		return fmt.Sprintf("%v", key)
	}
}

type Cache[K comparable, V any] interface {
	Set(k K, v V)
	Get(k K) (V, bool)
	GetAll() map[K]V
	Del(keys ...K) int
	Len() int
	Clear()
	// RefreshAll 原子替换缓存的全部内容: 按目标 map 逐 shard 替换,
	// 消除先 Clear 再 Set 之间的空窗口, 避免读者在刷新间隙看到空缓存。
	RefreshAll(target map[K]V)
}

func New[K comparable, V any](shards int) Cache[K, V] {
	if shards <= 0 {
		shards = 1024
	}
	// 分片路由按 hash&(shards-1) 位与计算, 非二次幂时高位不参与路由,
	// 部分分片永远不会被命中(如 shards=3 只用 0/2)。向上取整到最近的
	// 二次幂, 保证任意入参分布均匀。
	for shards&(shards-1) != 0 {
		shards = (shards | (shards - 1)) + 1
	}

	c := &cache[K, V]{
		shards:    make([]*shard[K, V], shards),
		shardMask: uint64(shards - 1),
	}
	for i := 0; i < shards; i++ {
		c.shards[i] = &shard[K, V]{hashmap: map[K]V{}}
	}

	return c
}

type cache[K comparable, V any] struct {
	shards    []*shard[K, V]
	shardMask uint64
	refreshMu sync.Mutex // 保护 RefreshAll: 跨 shard 替换操作原子性, 消除空窗口
}

func (c *cache[K, V]) Set(k K, v V) {
	hashedKey := xxhash.Sum64String(keyToString(k))
	shard := c.getShard(hashedKey)
	shard.set(k, v)
}

func (c *cache[K, V]) Get(k K) (V, bool) {
	hashedKey := xxhash.Sum64String(keyToString(k))
	shard := c.getShard(hashedKey)
	return shard.get(k)
}

func (c *cache[K, V]) GetAll() map[K]V {
	result := make(map[K]V)
	for _, shard := range c.shards {
		shardData := shard.snapshot()
		for k, v := range shardData {
			result[k] = v
		}
	}
	return result
}

func (c *cache[K, V]) Del(ks ...K) int {
	var count int
	for _, k := range ks {
		hashedKey := xxhash.Sum64String(keyToString(k))
		shard := c.getShard(hashedKey)
		count += shard.del(k)
	}
	return count
}

func (c *cache[K, V]) Len() int {
	var count int
	for _, shard := range c.shards {
		count += shard.size()
	}
	return count
}

func (c *cache[K, V]) getShard(hashedKey uint64) (shard *shard[K, V]) {
	return c.shards[hashedKey&c.shardMask]
}

func (c *cache[K, V]) Clear() {
	for _, s := range c.shards {
		s.clear()
	}
}

// RefreshAll 原子替换缓存的全部内容: 按目标 map 逐 shard 替换,
// 消除先 Clear 再 Set 之间的空窗口, 避免读者在刷新间隙看到空缓存。
// 持 refreshMu 串行化整个替换过程, 读者在替换期间仍走旧数据, 不会看到中间空态。
func (c *cache[K, V]) RefreshAll(target map[K]V) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	for _, shard := range c.shards {
		shard.clear()
	}
	for k, v := range target {
		c.Set(k, v)
	}
}
