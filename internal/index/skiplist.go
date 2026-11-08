package index

import (
	"math/bits"
	"sync/atomic"
)

const (
	maxLevel = 12
	nilNode  = int32(-1)
)

var rngSeedCounter atomic.Uint64

// skipNode contains zero pointers so Go's garbage collector marks the entire
// backing slice as noscan (zero GC mark scanning and zero pointer write barriers).
type skipNode struct {
	Timestamp int64
	ID        uint64
	forward   [maxLevel]int32
	prev      int32
	RecIdx    uint32
	topLevel  uint8
}

// SkipList is an arena-backed, zero-pointer doubly-linked skip list.
// Intended to be protected by its owning shard lock while active, and read lock-free once sealed.
type SkipList struct {
	nodes    []skipNode
	level    int
	length   int
	rngState uint64
}

func NewSkipList() *SkipList {
	seed := rngSeedCounter.Add(0x9e3779b97f4a7c15)
	if seed == 0 {
		seed = 0xfeedfacecafebabe
	}
	nodes := make([]skipNode, 1, 256)
	nodes[0] = skipNode{
		Timestamp: -1,
		ID:        0,
		prev:      nilNode,
		topLevel:  maxLevel,
	}
	for i := 0; i < maxLevel; i++ {
		nodes[0].forward[i] = nilNode
	}
	return &SkipList{
		nodes:    nodes,
		level:    1,
		rngState: seed,
	}
}

func (sl *SkipList) nextRand() uint64 {
	x := sl.rngState
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	sl.rngState = x
	return x * 0x2545F4914F6CDD1D
}

func (sl *SkipList) randomLevel() int {
	lvl := 1 + bits.TrailingZeros64(sl.nextRand())
	if lvl > maxLevel {
		return maxLevel
	}
	return lvl
}

func (sl *SkipList) Insert(timestamp int64, id uint64) {
	sl.InsertWithIdx(timestamp, id, 0)
}

func (sl *SkipList) InsertWithIdx(timestamp int64, id uint64, recIdx uint32) {
	var update [maxLevel]int32
	nodes := sl.nodes
	curr := int32(0)

	for i := sl.level - 1; i >= 0; i-- {
		for {
			next := nodes[curr].forward[i]
			if next == nilNode {
				break
			}
			nt := nodes[next].Timestamp
			if nt < timestamp || (nt == timestamp && nodes[next].ID < id) {
				curr = next
			} else {
				break
			}
		}
		update[i] = curr
	}

	lvl := sl.randomLevel()
	if lvl > sl.level {
		for i := sl.level; i < lvl; i++ {
			update[i] = 0
		}
		sl.level = lvl
	}

	newIdx := int32(len(sl.nodes))
	n := skipNode{
		Timestamp: timestamp,
		ID:        id,
		prev:      update[0],
		RecIdx:    recIdx,
		topLevel:  uint8(lvl),
	}
	for i := lvl; i < maxLevel; i++ {
		n.forward[i] = nilNode
	}
	sl.nodes = append(sl.nodes, n)
	nodes = sl.nodes

	for i := 0; i < lvl; i++ {
		u := update[i]
		nodes[newIdx].forward[i] = nodes[u].forward[i]
		nodes[u].forward[i] = newIdx
	}

	if next0 := nodes[newIdx].forward[0]; next0 != nilNode {
		nodes[next0].prev = newIdx
	}

	sl.length++
}

// First returns the minimum (Timestamp, ID) currently in the SkipList.
func (sl *SkipList) First() (int64, uint64, bool) {
	first := sl.nodes[0].forward[0]
	if first == nilNode {
		return 0, 0, false
	}
	return sl.nodes[first].Timestamp, sl.nodes[first].ID, true
}

// ScanRangeAscIdx walks nodes in [start, end] in strictly ascending (Timestamp, ID) order.
func (sl *SkipList) ScanRangeAscIdx(start, end int64, fn func(id uint64, ts int64, recIdx uint32) bool) {
	nodes := sl.nodes
	curr := int32(0)

	for i := sl.level - 1; i >= 0; i-- {
		for {
			next := nodes[curr].forward[i]
			if next == nilNode || nodes[next].Timestamp >= start {
				break
			}
			curr = next
		}
	}

	curr = nodes[curr].forward[0]
	for curr != nilNode && nodes[curr].Timestamp <= end {
		if !fn(nodes[curr].ID, nodes[curr].Timestamp, nodes[curr].RecIdx) {
			break
		}
		curr = nodes[curr].forward[0]
	}
}

// ScanRangeDescIdx walks nodes in [start, end] in strictly descending (Timestamp, ID) order.
func (sl *SkipList) ScanRangeDescIdx(start, end int64, fn func(id uint64, ts int64, recIdx uint32) bool) {
	nodes := sl.nodes
	curr := int32(0)

	for i := sl.level - 1; i >= 0; i-- {
		for {
			next := nodes[curr].forward[i]
			if next == nilNode || nodes[next].Timestamp > end {
				break
			}
			curr = next
		}
	}

	for curr != 0 && curr != nilNode && nodes[curr].Timestamp >= start {
		if !fn(nodes[curr].ID, nodes[curr].Timestamp, nodes[curr].RecIdx) {
			break
		}
		curr = nodes[curr].prev
	}
}

// ScanRangeAsc walks nodes in [start, end] in strictly ascending (Timestamp, ID) order.
func (sl *SkipList) ScanRangeAsc(start, end int64, fn func(id uint64, ts int64) bool) {
	sl.ScanRangeAscIdx(start, end, func(id uint64, ts int64, _ uint32) bool {
		return fn(id, ts)
	})
}

// ScanRangeDesc walks nodes in [start, end] in strictly descending (Timestamp, ID) order.
func (sl *SkipList) ScanRangeDesc(start, end int64, fn func(id uint64, ts int64) bool) {
	sl.ScanRangeDescIdx(start, end, func(id uint64, ts int64, _ uint32) bool {
		return fn(id, ts)
	})
}

// ScanRange maintains compatibility with original signature.
func (sl *SkipList) ScanRange(start, end int64, fn func(id uint64) bool) {
	sl.ScanRangeAscIdx(start, end, func(id uint64, _ int64, _ uint32) bool {
		return fn(id)
	})
}

// EvictOldestIdx removes up to `count` oldest entries from the head and returns their RecIdx slice.
func (sl *SkipList) EvictOldestIdx(count int) []uint32 {
	if sl.length == 0 || count <= 0 {
		return nil
	}

	nodes := sl.nodes
	var evicted []uint32
	for nodes[0].forward[0] != nilNode && len(evicted) < count {
		evictIdx := nodes[0].forward[0]
		evicted = append(evicted, nodes[evictIdx].RecIdx)

		top := int(nodes[evictIdx].topLevel)
		for i := 0; i < top; i++ {
			if nodes[0].forward[i] == evictIdx {
				nodes[0].forward[i] = nodes[evictIdx].forward[i]
			}
		}

		if next0 := nodes[evictIdx].forward[0]; next0 != nilNode {
			nodes[next0].prev = 0
		}

		sl.length--
	}

	for sl.level > 1 && nodes[0].forward[sl.level-1] == nilNode {
		sl.level--
	}

	return evicted
}

func (sl *SkipList) Len() int {
	return sl.length
}
