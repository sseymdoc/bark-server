package sse

import (
	"strconv"
	"testing"
	"time"
)

func reset(t *testing.T, start time.Time) *time.Time {
	clock := start
	now = func() time.Time { return clock }
	devices = map[string]*device{}
	prevID = 0
	t.Cleanup(func() { now = time.Now })
	return &clock
}

func TestPublishSubscribe(t *testing.T) {
	reset(t, time.Unix(1000, 0))
	first := Publish("k", map[string]string{"body": "a"})
	id, _ := strconv.ParseInt(first, 10, 64)

	// A reconnecting client gets only what is newer than its Last-Event-ID.
	backlog, events, cancel := Subscribe("k", 0)
	if len(backlog) != 1 || backlog[0].Data["body"] != "a" || backlog[0].Data["_msgid"] != first {
		t.Fatalf("backlog = %+v", backlog)
	}
	if b, _, c := Subscribe("k", id); len(b) != 0 {
		t.Fatalf("backlog after last id = %+v", b)
	} else {
		c()
	}

	// Live delivery, with ids increasing even when the clock does not move.
	second := Publish("k", map[string]string{"body": "b"})
	if second <= first {
		t.Fatalf("ids not increasing: %s then %s", first, second)
	}
	select {
	case e := <-events:
		if e.Data["body"] != "b" {
			t.Fatalf("event = %+v", e)
		}
	default:
		t.Fatal("no live event")
	}

	// Other keys are not affected; cancelled subscribers get nothing.
	cancel()
	Publish("other", map[string]string{"body": "c"})
	Publish("k", map[string]string{"body": "d"})
	select {
	case e := <-events:
		t.Fatalf("cancelled subscriber got %+v", e)
	default:
	}
}

func TestBacklogLimits(t *testing.T) {
	clock := reset(t, time.Unix(1000, 0))
	for i := 0; i < maxBacklog+5; i++ {
		Publish("k", map[string]string{"n": strconv.Itoa(i)})
	}
	backlog, _, cancel := Subscribe("k", 0)
	cancel()
	if len(backlog) != maxBacklog || backlog[0].Data["n"] != "5" {
		t.Fatalf("len = %d, first = %v", len(backlog), backlog[0].Data)
	}

	*clock = clock.Add(maxAge + time.Second)
	if backlog, _, cancel := Subscribe("k", 0); len(backlog) != 0 {
		t.Fatalf("expired events replayed: %d", len(backlog))
	} else {
		cancel()
	}
	Cleanup()
	if len(devices) != 0 {
		t.Fatalf("devices left after cleanup: %d", len(devices))
	}
}
