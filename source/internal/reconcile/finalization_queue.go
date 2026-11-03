package reconcile

import (
	"container/heap"
	"time"
)

type finalizationQueueEntry struct {
	key         finalizationKey
	nextAttempt time.Time
	index       int
}

type finalizationQueue []*finalizationQueueEntry

func (q finalizationQueue) Len() int { return len(q) }

func (q finalizationQueue) Less(i, j int) bool {
	if !q[i].nextAttempt.Equal(q[j].nextAttempt) {
		return q[i].nextAttempt.Before(q[j].nextAttempt)
	}
	if q[i].key.minute != q[j].key.minute {
		return q[i].key.minute < q[j].key.minute
	}
	return q[i].key.exchange < q[j].key.exchange
}

func (q finalizationQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *finalizationQueue) Push(value any) {
	entry := value.(*finalizationQueueEntry)
	entry.index = len(*q)
	*q = append(*q, entry)
}

func (q *finalizationQueue) Pop() any {
	old := *q
	last := len(old) - 1
	value := old[last]
	old[last] = nil
	*q = old[:last]
	value.index = -1
	return value
}

func (q *finalizationQueue) push(entry *finalizationQueueEntry) {
	heap.Push(q, entry)
}

func (q *finalizationQueue) remove(entry *finalizationQueueEntry) {
	if entry != nil && entry.index >= 0 && entry.index < q.Len() && (*q)[entry.index] == entry {
		heap.Remove(q, entry.index)
	}
}

func (q *finalizationQueue) update(entry *finalizationQueueEntry, nextAttempt time.Time) {
	entry.nextAttempt = nextAttempt
	if entry.index >= 0 && entry.index < q.Len() && (*q)[entry.index] == entry {
		heap.Fix(q, entry.index)
	}
}
