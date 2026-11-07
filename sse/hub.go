// Package sse fans Android pushes out to apps connected over Server-Sent Events, and keeps a short
// in-memory backlog per device so an app that reconnects can catch up on what it missed. The app
// also receives the same messages over FCM and drops duplicates by "_msgid".
package sse

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxBacklog = 100
	maxAge     = 24 * time.Hour
)

// Event is one push. Data is the same payload FCM carries, including "_msgid" (the decimal ID).
type Event struct {
	ID   int64
	Time time.Time
	Data map[string]string
}

type device struct {
	subs    map[chan Event]struct{}
	backlog []Event
}

var (
	mu      sync.Mutex
	devices = map[string]*device{}
	prevID  int64
	now     = time.Now // overridden in tests
)

// get returns key's device, creating it. Strings from fiber (params, query, body) point into request
// buffers that are reused after the handler returns, so anything kept beyond the request is cloned.
func get(key string) *device {
	d := devices[key]
	if d == nil {
		d = &device{subs: map[chan Event]struct{}{}}
		devices[strings.Clone(key)] = d
	}
	return d
}

// trim drops events beyond the backlog size or older than maxAge.
func (d *device) trim(t time.Time) {
	i := 0
	for i < len(d.backlog) && (len(d.backlog)-i > maxBacklog || t.Sub(d.backlog[i].Time) > maxAge) {
		i++
	}
	d.backlog = d.backlog[i:]
}

// Publish stores data for key and hands it to every connected subscriber. It sets data["_msgid"]
// and returns it. IDs are Unix nanoseconds, so they keep increasing across server restarts and an
// app's Last-Event-ID stays meaningful.
func Publish(key string, data map[string]string) string {
	mu.Lock()
	defer mu.Unlock()
	t := now()
	id := t.UnixNano()
	if id <= prevID {
		id = prevID + 1
	}
	prevID = id
	data["_msgid"] = strconv.FormatInt(id, 10)
	stored := make(map[string]string, len(data))
	for k, v := range data {
		stored[strings.Clone(k)] = strings.Clone(v)
	}
	e := Event{ID: id, Time: t, Data: stored}
	d := get(key)
	d.backlog = append(d.backlog, e)
	d.trim(t)
	for ch := range d.subs {
		select {
		case ch <- e:
		default: // a stalled subscriber gets it from the backlog when it reconnects
		}
	}
	return data["_msgid"]
}

// Subscribe returns the stored events newer than lastID and a channel for new ones.
// cancel must be called when the client goes away.
func Subscribe(key string, lastID int64) (backlog []Event, events <-chan Event, cancel func()) {
	mu.Lock()
	defer mu.Unlock()
	d := get(key)
	d.trim(now())
	for _, e := range d.backlog {
		if e.ID > lastID {
			backlog = append(backlog, e)
		}
	}
	ch := make(chan Event, 16)
	d.subs[ch] = struct{}{}
	return backlog, ch, func() {
		mu.Lock()
		defer mu.Unlock()
		delete(d.subs, ch)
	}
}

// Cleanup forgets devices with no subscribers and nothing left in their backlog.
func Cleanup() {
	mu.Lock()
	defer mu.Unlock()
	t := now()
	for key, d := range devices {
		d.trim(t)
		if len(d.subs) == 0 && len(d.backlog) == 0 {
			delete(devices, key)
		}
	}
}
