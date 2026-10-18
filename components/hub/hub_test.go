package hub

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// Номер событий продолжается после перезапуска (от времени), а не с нуля.
func TestSeqStartsFromTime(t *testing.T) {
	if seq < time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("seq начинается не от времени: %d", seq)
	}
}

// Push не уходит в паузе между опросами; уходит, если человек давно не опрашивал.
func TestOfflineOnlyAfterPause(t *testing.T) {
	var pushed atomic.Int32
	Offline = func(int64, string, json.RawMessage) { pushed.Add(1) }
	defer func() { Offline = nil }()

	const uid = 777001
	ch := Subscribe(uid)
	Unsubscribe(uid, ch) // только что опрашивал
	Send(uid, "message", map[string]int{"x": 1})
	time.Sleep(20 * time.Millisecond)
	if pushed.Load() != 0 {
		t.Fatal("push ушёл в паузе между long-poll")
	}

	mu.Lock()
	seen[uid] = time.Now().Add(-time.Minute) // давно не появлялся
	mu.Unlock()
	Send(uid, "message", map[string]int{"x": 2})
	time.Sleep(20 * time.Millisecond)
	if pushed.Load() != 1 {
		t.Fatalf("push не ушёл офлайн-пользователю: %d", pushed.Load())
	}
}

// Чистка убирает события старше keep и пустые ключи, свежие оставляет.
func TestCleanup(t *testing.T) {
	const uid = 777002
	Send(uid, "like", nil)
	Send(uid, "like", nil)
	if evs, _ := Since(uid, 0); len(evs) != 2 {
		t.Fatalf("в буфере: %d", len(evs))
	}
	cleanup(time.Now())
	if evs, _ := Since(uid, 0); len(evs) != 2 {
		t.Fatal("свежие события удалены")
	}
	cleanup(time.Now().Add(keep + time.Second))
	mu.Lock()
	_, left := buf[uid]
	mu.Unlock()
	if left {
		t.Fatal("старые события не вычищены")
	}
}
