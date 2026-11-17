package events

import (
	"sync"
	"testing"
	"time"
)

func TestPublishSubscribeAndSince(t *testing.T) {
	b := New(3)
	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	tick := 0
	b.now = func() time.Time { tick++; return t0.Add(time.Duration(tick) * time.Minute) }
	for _, id := range []string{"a", "b", "c", "d"} { // "a" falls out of the 3-slot ring
		b.Publish(Event{Type: TypeContainer, Action: Start, ID: id})
	}
	past, sub := b.Subscribe(t0, nil)
	defer sub.Close()
	if len(past) != 3 || past[0].ID != "b" || past[2].ID != "d" {
		t.Fatalf("history: %+v", past)
	}
	if past, s2 := b.Subscribe(t0.Add(3*time.Minute), Filter{"container": {"d"}}); len(past) != 1 || past[0].ID != "d" {
		t.Fatalf("since + filter: %+v", past)
	} else {
		s2.Close()
		s2.Close() // idempotent
	}
	if past, s3 := b.Subscribe(time.Time{}, nil); len(past) != 0 {
		t.Fatalf("no since means no history: %+v", past)
	} else {
		s3.Close()
	}
	b.Publish(Event{Type: TypeContainer, Action: Die, ID: "e"})
	select {
	case e := <-sub.C:
		if e.ID != "e" || e.Action != Die || e.Time.IsZero() {
			t.Fatalf("live: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no live event")
	}
	var nilBus *Bus
	nilBus.Publish(Event{}) // no-op
}

// A subscriber that never reads must not block the publisher (the
// supervisor): it is dropped and its channel closed.
func TestStuckSubscriberIsDropped(t *testing.T) {
	b := New(10)
	_, stuck := b.Subscribe(time.Time{}, nil)
	_, other := b.Subscribe(time.Time{}, Filter{"event": {Die}})
	var got int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range other.C {
			got++
		}
	}()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5*subscriberBuffer; i++ {
			b.Publish(Event{Type: TypeContainer, Action: Start, ID: "x"})
		}
		b.Publish(Event{Type: TypeContainer, Action: Die, ID: "x"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked on a stuck subscriber")
	}
	if !stuck.Dropped() {
		t.Fatal("stuck subscriber not dropped")
	}
	n := 0
	for range stuck.C { // buffered events, then closed
		n++
	}
	if n != subscriberBuffer {
		t.Fatalf("stuck got %d buffered events", n)
	}
	other.Close()
	wg.Wait()
	if got != 1 || other.Dropped() {
		t.Fatalf("filtered subscriber: got %d, dropped %v", got, other.Dropped())
	}
}

func TestFilter(t *testing.T) {
	f, err := ParseFilter([]string{"container=web", "container=db", "event=die"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		e    Event
		want bool
	}{
		{Event{Type: TypeContainer, Action: Die, ID: "web"}, true},
		{Event{Type: TypeContainer, Action: Die, ID: "db"}, true},
		{Event{Type: TypeContainer, Action: Start, ID: "web"}, false},
		{Event{Type: TypeContainer, Action: Die, ID: "cache"}, false},
	} {
		if f.Match(c.e) != c.want {
			t.Errorf("%+v: want %v", c.e, c.want)
		}
	}
	img, _ := ParseFilter([]string{"type=image", "image=alpine:3.20"})
	if !img.Match(Event{Type: TypeImage, Action: ImagePull, ID: "alpine:3.20"}) || img.Match(Event{Type: TypeContainer, Image: "alpine:3.20"}) {
		t.Error("image filter")
	}
	ctr, _ := ParseFilter([]string{"image=alpine:3.20"})
	if !ctr.Match(Event{Type: TypeContainer, Action: Start, ID: "web", Image: "alpine:3.20"}) {
		t.Error("container by image")
	}
	for _, bad := range []string{"container", "color=red", "event="} {
		if _, err := ParseFilter([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if (Filter(nil)).Match(Event{}) != true || New(0) == nil {
		t.Error("nil filter / default history")
	}
}
