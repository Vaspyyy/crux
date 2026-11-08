package calc

import "testing"

func check(t *testing.T, got, want int) {
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestAdd(t *testing.T) {
	var a Acc
	check(t, a.Add(2), 3)
}

func TestDiv(t *testing.T) {
	t.Run("negative", func(t *testing.T) {
		if got := Div(-4, 2); got != 2 {
			t.Errorf("Div(-4, 2) = %d, want 2", got)
		}
	})
}

func TestNoMessage(t *testing.T) {
	t.Fail()
}
