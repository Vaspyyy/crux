package calc

// Acc adds numbers up.
type Acc struct{ n int }

// Add adds x and returns the total.
func (a *Acc) Add(x int) int {
	a.n += x
	return a.n
}

// Div divides a by b.
func Div(a, b int) int {
	return a / b
}
