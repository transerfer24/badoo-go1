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
	"time"
)

type Event struct {
	Seq  int64           `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	at   time.Time       // когда отправлено — для чистки буфера
}

const (
	buffer = 100              // событий в буфере на пользователя
	keep   = 10 * time.Minute // дольше в буфере не держим: клиент, который пропал на 10 минут, перечитает всё с сервера
	online = 35 * time.Second // столько после последнего опроса человек считается «в приложении» (long-poll — 25 с)
)

var (
	mu sync.Mutex
	// seq — номер последнего события. Начинается с текущего времени в мс, а не с нуля: после перезапуска (выкладка,
	// keepalive) номера продолжают расти, и приложение с since=N из прошлого процесса не «ждёт» номеров, которых не будет.
	seq  = time.Now().UnixMilli()
	subs = map[int64]map[chan Event]struct{}{}
	buf  = map[int64][]Event{}
	// seen — когда у пользователя последний раз была открыта подписка (long-poll приложения переподключается каждые
	// 25 с, SSE сайта — раз в минуту): в паузе между запросами человек всё ещё в приложении, push не нужен.
	seen = map[int64]time.Time{}
	// Offline — хук для push: событие ушло пользователю без открытых подписок.
	Offline func(uid int64, typ string, data json.RawMessage)
)

// чистка раз в минуту: старые события и давно не появлявшиеся пользователи — иначе память растёт со всеми,
// кто когда-либо получал событие
func init() {
	go func() {
		for range time.Tick(time.Minute) {
			cleanup(time.Now())
		}
	}()
}

func cleanup(now time.Time) {
	mu.Lock()
	defer mu.Unlock()
	for uid, b := range buf {
		i := 0
		for i < len(b) && now.Sub(b[i].at) > keep {
			i++
		}
		if i == len(b) {
			delete(buf, uid)
		} else if i > 0 {
			buf[uid] = append([]Event(nil), b[i:]...)
		}
	}
	for uid, t := range seen {
		if _, open := subs[uid]; !open && now.Sub(t) > keep {
			delete(seen, uid)
		}
	}
}

func Subscribe(uid int64) chan Event {
	ch := make(chan Event, 16)
	mu.Lock()
	if subs[uid] == nil {
		subs[uid] = map[chan Event]struct{}{}
	}
	subs[uid][ch] = struct{}{}
	seen[uid] = time.Now()
	mu.Unlock()
	return ch
}

func Unsubscribe(uid int64, ch chan Event) {
	mu.Lock()
	delete(subs[uid], ch)
	if len(subs[uid]) == 0 {
		delete(subs, uid)
	}
	seen[uid] = time.Now()
	mu.Unlock()
}

// Send — событие пользователю: в буфер, подписчикам, офлайн-хуку.
func Send(uid int64, typ string, payload any) {
	data, _ := json.Marshal(payload)
	mu.Lock()
	defer mu.Unlock()
	seq++
	ev := Event{Seq: seq, Type: typ, Data: data, at: time.Now()}
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
	// push — только если подписок нет и человек не опрашивал недавно (не пауза между long-poll)
	if len(subs[uid]) == 0 && time.Since(seen[uid]) > online && Offline != nil {
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
