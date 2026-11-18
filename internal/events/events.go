// Package events is nyxd's event bus: container and image events kept in a
// fixed-size ring in memory and streamed to subscribers (GET /v1/events,
// nyx events). Nothing is written to disk. Publishing never blocks: a
// subscriber that cannot keep up is dropped, so a stuck client cannot stall
// the supervisor.
package events

import (
	"strings"
	"sync"
	"time"
)

// Types of events.
const (
	TypeContainer = "container"
	TypeImage     = "image"
)

// Container actions.
const (
	Create       = "create"
	Start        = "start"
	Die          = "die"
	OOM          = "oom"
	Restart      = "restart"
	HealthStatus = "health_status"
	Remove       = "remove"
	// Update and UpdateRolledBack come from automatic image updates.
	Update           = "update"
	UpdateRolledBack = "update_rolled_back"
)

// Image actions.
const (
	ImagePull   = "pull"
	ImageRemove = "remove"
	ImageBuild  = "build"
)

// Event is one thing that happened. ID is the container id (its name) or
// the image reference.
type Event struct {
	Time   time.Time         `json:"time"`
	Type   string            `json:"type"`
	Action string            `json:"action"`
	ID     string            `json:"id,omitempty"`
	Image  string            `json:"image,omitempty"`
	Attrs  map[string]string `json:"attributes,omitempty"`
}

// DefaultHistory is how many events a bus keeps for since= replays.
const DefaultHistory = 1000

// subscriberBuffer is how many events a subscriber may lag behind before it
// is dropped.
const subscriberBuffer = 256

// Bus fans events out to subscribers and keeps the last few in a ring. The
// zero value is not usable; a nil *Bus ignores Publish.
type Bus struct {
	mu   sync.Mutex
	ring []Event
	next int  // ring index of the next event
	full bool // the ring has wrapped
	subs map[*Subscription]struct{}
	now  func() time.Time
}

// New returns a bus keeping the last history events.
func New(history int) *Bus {
	if history < 1 {
		history = DefaultHistory
	}
	return &Bus{ring: make([]Event, history), subs: map[*Subscription]struct{}{}, now: time.Now}
}

// Publish records e and sends it to every subscriber whose filter matches.
// A subscriber whose buffer is full is dropped instead of waited for.
func (b *Bus) Publish(e Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = b.now().UTC()
	}
	b.ring[b.next] = e
	b.next = (b.next + 1) % len(b.ring)
	if b.next == 0 {
		b.full = true
	}
	for s := range b.subs {
		if !s.match(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			s.dropped = true
			b.unsubscribeLocked(s)
		}
	}
}

// Subscription receives events until Close, or until it falls behind
// (C is closed and Dropped reports true).
type Subscription struct {
	C       <-chan Event
	ch      chan Event
	match   func(Event) bool
	bus     *Bus
	dropped bool
}

// Subscribe returns the kept events newer than since that match f (oldest
// first) and a subscription for the ones to come. A nil f matches all.
func (b *Bus) Subscribe(since time.Time, f Filter) ([]Event, *Subscription) {
	ch := make(chan Event, subscriberBuffer)
	s := &Subscription{C: ch, ch: ch, match: f.Match, bus: b}
	b.mu.Lock()
	defer b.mu.Unlock()
	var past []Event
	if !since.IsZero() {
		n := b.next
		if b.full {
			n = len(b.ring)
		}
		for i := 0; i < n; i++ {
			idx := i
			if b.full {
				idx = (b.next + i) % len(b.ring)
			}
			if e := b.ring[idx]; e.Time.After(since) && f.Match(e) {
				past = append(past, e)
			}
		}
	}
	b.subs[s] = struct{}{}
	return past, s
}

// Close ends the subscription.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.bus.unsubscribeLocked(s)
}

// Dropped reports whether the subscription was ended for falling behind.
func (s *Subscription) Dropped() bool {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.dropped
}

func (b *Bus) unsubscribeLocked(s *Subscription) {
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.ch)
	}
}

// Filter selects events, docker style: values of one key are alternatives,
// different keys must all match. Keys: type, event (the action), container
// (id), image.
type Filter map[string][]string

// ParseFilter reads "key=value" arguments.
func ParseFilter(args []string) (Filter, error) {
	f := Filter{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || v == "" {
			return nil, &FilterError{a}
		}
		switch k {
		case "type", "event", "container", "image":
		default:
			return nil, &FilterError{a}
		}
		f[k] = append(f[k], v)
	}
	return f, nil
}

// FilterError is an unusable filter argument.
type FilterError struct{ Arg string }

func (e *FilterError) Error() string {
	return "invalid filter " + e.Arg + " (want type=, event=, container= or image=)"
}

// Match reports whether e passes the filter.
func (f Filter) Match(e Event) bool {
	field := map[string]string{"type": e.Type, "event": e.Action, "container": "", "image": e.Image}
	if e.Type == TypeContainer {
		field["container"] = e.ID
	} else {
		field["image"] = e.ID
	}
	for k, vals := range f {
		ok := false
		for _, v := range vals {
			if field[k] == v {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
