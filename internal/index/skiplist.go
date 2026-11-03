package index

import (
	"math/rand"
	"time"
)

const (
	maxLevel    = 16
	probability = 0.5
)

type SkipNode struct {
	Timestamp int64
	ID        uint64
	forward   []*SkipNode
	prev      *SkipNode // Doubly linked on level 0 for O(1) reverse traversal
}

// SkipList without internal lock - intended to be protected by partition lock.
type SkipList struct {
	head   *SkipNode
	level  int
	length int
	rnd    *rand.Rand
}

func NewSkipList() *SkipList {
	return &SkipList{
		head: &SkipNode{
			Timestamp: -1,
			ID:        0,
			forward:   make([]*SkipNode, maxLevel),
		},
		level: 1,
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (sl *SkipList) randomLevel() int {
	lvl := 1
	for lvl < maxLevel && sl.rnd.Float64() < probability {
		lvl++
	}
	return lvl
}

func (sl *SkipList) Insert(timestamp int64, id uint64) {
	update := make([]*SkipNode, maxLevel)
	current := sl.head

	for i := sl.level - 1; i >= 0; i-- {
		for current.forward[i] != nil &&
			(current.forward[i].Timestamp < timestamp ||
				(current.forward[i].Timestamp == timestamp && current.forward[i].ID < id)) {
			current = current.forward[i]
		}
		update[i] = current
	}

	lvl := sl.randomLevel()
	if lvl > sl.level {
		for i := sl.level; i < lvl; i++ {
			update[i] = sl.head
		}
		sl.level = lvl
	}

	newNode := &SkipNode{
		Timestamp: timestamp,
		ID:        id,
		forward:   make([]*SkipNode, lvl),
	}

	for i := 0; i < lvl; i++ {
		newNode.forward[i] = update[i].forward[i]
		update[i].forward[i] = newNode
	}

	// Maintain doubly linked list on level 0
	newNode.prev = update[0]
	if newNode.forward[0] != nil {
		newNode.forward[0].prev = newNode
	}

	sl.length++
}

// ScanRangeAsc walks nodes in [start, end] in strictly ascending (Timestamp, ID) order.
func (sl *SkipList) ScanRangeAsc(start, end int64, fn func(id uint64, ts int64) bool) {
	current := sl.head

	for i := sl.level - 1; i >= 0; i-- {
		for current.forward[i] != nil && current.forward[i].Timestamp < start {
			current = current.forward[i]
		}
	}

	current = current.forward[0]
	for current != nil && current.Timestamp <= end {
		if !fn(current.ID, current.Timestamp) {
			break
		}
		current = current.forward[0]
	}
}

// ScanRangeDesc walks nodes in [start, end] in strictly descending (Timestamp, ID) order.
func (sl *SkipList) ScanRangeDesc(start, end int64, fn func(id uint64, ts int64) bool) {
	current := sl.head

	// Binary jump to the largest node <= end
	for i := sl.level - 1; i >= 0; i-- {
		for current.forward[i] != nil && current.forward[i].Timestamp <= end {
			current = current.forward[i]
		}
	}

	for current != nil && current != sl.head && current.Timestamp >= start {
		if !fn(current.ID, current.Timestamp) {
			break
		}
		current = current.prev
	}
}

// ScanRange maintains compatibility with original signature
func (sl *SkipList) ScanRange(start, end int64, fn func(id uint64) bool) {
	sl.ScanRangeAsc(start, end, func(id uint64, ts int64) bool {
		return fn(id)
	})
}

// EvictOldest removes up to `count` oldest entries from the head of the SkipList and returns their IDs.
func (sl *SkipList) EvictOldest(count int) []uint64 {
	if sl.length == 0 || count <= 0 {
		return nil
	}

	var evicted []uint64
	for sl.head.forward[0] != nil && len(evicted) < count {
		nodeToEvict := sl.head.forward[0]
		evicted = append(evicted, nodeToEvict.ID)

		// Unlink nodeToEvict from all levels it participates in
		for i := 0; i < len(nodeToEvict.forward); i++ {
			if sl.head.forward[i] == nodeToEvict {
				sl.head.forward[i] = nodeToEvict.forward[i]
			}
		}

		if nodeToEvict.forward[0] != nil {
			nodeToEvict.forward[0].prev = sl.head
		}

		sl.length--
	}

	// Adjust sl.level if top levels are now empty
	for sl.level > 1 && sl.head.forward[sl.level-1] == nil {
		sl.level--
	}

	return evicted
}

func (sl *SkipList) Len() int {
	return sl.length
}
