// Package hub — события для пользователя в реальном времени (перенос backend/hub.go):
// сообщение, мэтч, лайк, прочитано. Веб-клиент слушает SSE (/api/events), мобильное приложение —
// long-poll (/api/v1/events?since=N). Последние события лежат в памяти, чтобы клиент не терял их между
// запросами. Один процесс на shared-хостинге — одного хаба в памяти достаточно (при двух инстансах —
// переехать на ws.RedisBroker ядра).
//
// Offline вызывается, когда событие пришло человеку, у которого сейчас нет открытой ленты
// (приложение закрыто или свёрнуто): отсюда шлём push (components/push, этап 5).
package hub

import (
	"encoding/json"
	"sync"
)

type Event struct {
	Seq  int64           `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

const buffer = 100

var (
	mu   sync.Mutex
	seq  int64
	subs = map[int64]map[chan Event]struct{}{}
	buf  = map[int64][]Event{}
	// Offline — хук для push: событие ушло пользователю без открытых подписок.
	Offline func(uid int64, typ string, data json.RawMessage)
)

func Subscribe(uid int64) chan Event {
	ch := make(chan Event, 16)
	mu.Lock()
	if subs[uid] == nil {
		subs[uid] = map[chan Event]struct{}{}
	}
	subs[uid][ch] = struct{}{}
	mu.Unlock()
	return ch
}

func Unsubscribe(uid int64, ch chan Event) {
	mu.Lock()
	delete(subs[uid], ch)
	if len(subs[uid]) == 0 {
		delete(subs, uid)
	}
	mu.Unlock()
}

// Send — событие пользователю: в буфер, подписчикам, офлайн-хуку.
func Send(uid int64, typ string, payload any) {
	data, _ := json.Marshal(payload)
	mu.Lock()
	defer mu.Unlock()
	seq++
	ev := Event{Seq: seq, Type: typ, Data: data}
	b := append(buf[uid], ev)
	if len(b) > buffer {
		b = b[len(b)-buffer:]
	}
	buf[uid] = b
	for ch := range subs[uid] {
		select {
		case ch <- ev:
		default: // медленный клиент — догрузит из буфера
		}
	}
	if len(subs[uid]) == 0 && Offline != nil {
		go Offline(uid, typ, data)
	}
}

// Since — события пользователя новее seq и текущий последний номер.
func Since(uid, after int64) ([]Event, int64) {
	mu.Lock()
	defer mu.Unlock()
	out := []Event{}
	for _, e := range buf[uid] {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out, seq
}
