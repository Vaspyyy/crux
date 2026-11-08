package broken

var limit = undefinedLimit

// Queue holds items in order.
type Queue struct{ items []int }

// Push adds x at the end.
func (q *Queue) Push(x int) {
	q.items = append(q.items, y)
}

// Len is the number of items.
func Len(q *Queue) int {
	return len(q.itemz)
}
