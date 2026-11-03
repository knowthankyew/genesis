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
	sl.length++
}

func (sl *SkipList) ScanRange(start, end int64, fn func(id uint64) bool) {
	current := sl.head

	for i := sl.level - 1; i >= 0; i-- {
		for current.forward[i] != nil && current.forward[i].Timestamp < start {
			current = current.forward[i]
		}
	}

	current = current.forward[0]
	for current != nil && current.Timestamp <= end {
		if !fn(current.ID) {
			break
		}
		current = current.forward[0]
	}
}

func (sl *SkipList) Range(start, end int64) []uint64 {
	var result []uint64
	sl.ScanRange(start, end, func(id uint64) bool {
		result = append(result, id)
		return true
	})
	return result
}

func (sl *SkipList) Len() int {
	return sl.length
}
