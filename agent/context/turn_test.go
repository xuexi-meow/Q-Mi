package context

import (
	"reflect"
	"sync"
	"testing"
)

func TestTurnPendingImagesCanBeSnapshottedAndCleared(t *testing.T) {
	var turn Turn
	turn.AddPendingImage("data:image/png;base64,one")
	turn.AddPendingImage("data:image/jpeg;base64,two")

	want := []string{"data:image/png;base64,one", "data:image/jpeg;base64,two"}
	if got := turn.PendingImages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingImages() = %v, want %v", got, want)
	}
	turn.ClearPendingImages()
	if got := turn.PendingImages(); len(got) != 0 {
		t.Fatalf("PendingImages() after clear = %v, want empty", got)
	}
}

func TestTurnPendingImagesSupportsConcurrentAppend(t *testing.T) {
	var turn Turn
	const count = 32
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func(i int) {
			defer wg.Done()
			turn.AddPendingImage(string(rune('a' + i)))
		}(i)
	}
	wg.Wait()
	if got := len(turn.PendingImages()); got != count {
		t.Fatalf("PendingImages() length = %d, want %d", got, count)
	}
}
