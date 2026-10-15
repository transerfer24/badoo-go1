package main

// Встречи и push (этап 5): создание → список рядом → «Пойду» → участники → «Я здесь» → отмена; анкета с общими
// событиями; пуш уходит в Expo (подменный сервер) через очередь, когда у человека нет открытой ленты.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"badoo/components/push"

	"whales.team/go1/app"
	"whales.team/go1/queue"
)

func TestMeetupsAndPush(t *testing.T) {
	fake := &fakeNeuronet{secret: "s3cr3t", accounts: map[string]string{}, uids: map[string]int{}, sent: map[string]int{}}
	nsrv := httptest.NewServer(fake)
	defer nsrv.Close()

	// подменный Expo: запоминает пачки, второй токен объявляет «устройство не зарегистрировано»
	var mu sync.Mutex
	var got [][]push.Msg
	expo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []push.Msg
		_ = json.NewDecoder(r.Body).Decode(&batch)
		mu.Lock()
		got = append(got, batch)
		mu.Unlock()
		data := []map[string]any{}
		for _, m := range batch {
			if strings.Contains(m.To, "dead") {
				data = append(data, map[string]any{"status": "error", "details": map[string]string{"error": "DeviceNotRegistered"}})
			} else {
				data = append(data, map[string]any{"status": "ok"})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer expo.Close()

	h := newTestApp(t, nsrv.URL)
	app.Params["push.enabled"], app.Params["push.url"] = true, expo.URL

	anna := register(t, h, "anna@n.io", "Анна")
	onboard(t, anna, "Анна", "f", "m")
	boris := register(t, h, "boris@n.io", "Борис")
	bme := onboard(t, boris, "Борис", "m", "f")
	var okr map[string]bool
	boris.do("POST", "/api/v1/me/push", map[string]string{"token": "ExponentPushToken[live]", "platform": "ios"}, &okr)
	boris.do("POST", "/api/v1/me/push", map[string]string{"token": "ExponentPushToken[dead]", "platform": "android"}, &okr)

	// ---- встречи ----
	var e map[string]string
	if code := anna.do("POST", "/api/v1/meetups", map[string]any{"title": "Йо", "kind": "walk", "place": "Парк", "startsAt": time.Now().Add(2 * time.Hour).Format(time.RFC3339)}, &e); code != 400 || e["error"] != "title" {
		t.Fatalf("валидация события: %d %v", code, e)
	}
	var created struct {
		Meetup struct {
			Id    int64                  `json:"id"`
			Going int                    `json:"going"`
			IGo   bool                   `json:"iGo"`
			Host  *struct{ Name string } `json:"host"`
			Faces []any                  `json:"faces"`
			Dist  string                 `json:"dist"`
		} `json:"meetup"`
	}
	in := map[string]any{"title": "Прогулка по Ретиро", "about": "Неспешно", "kind": "walk", "place": "Вход у Пуэрта-де-Алькала",
		"startsAt": time.Now().Add(30 * time.Minute).Format(time.RFC3339), "hours": 2, "capacity": 2}
	if code := anna.do("POST", "/api/v1/meetups", in, &created); code != 200 || created.Meetup.Id == 0 || !created.Meetup.IGo || created.Meetup.Going != 1 || created.Meetup.Host == nil || created.Meetup.Host.Name != "Анна" {
		t.Fatalf("создание: %d %+v", code, created)
	}
	mid := itoa(created.Meetup.Id)

	var list struct {
		Meetups []map[string]any `json:"meetups"`
		Kinds   []any            `json:"kinds"`
	}
	if code := boris.do("GET", "/api/v1/meetups?days=7", nil, &list); code != 200 || len(list.Meetups) != 1 || list.Meetups[0]["iGo"] != false || len(list.Kinds) != 8 {
		t.Fatalf("список рядом: %d %+v", code, list)
	}
	var view struct {
		Meetup map[string]any   `json:"meetup"`
		People []map[string]any `json:"people"`
	}
	boris.do("GET", "/api/v1/meetups/"+mid, nil, &view)
	if len(view.People) != 0 { // пока не идёт — участников не видит
		t.Fatalf("участники чужому: %+v", view)
	}
	if code := boris.do("POST", "/api/v1/meetups/"+mid+"/join", nil, &created); code != 200 || !created.Meetup.IGo || created.Meetup.Going != 2 || len(created.Meetup.Faces) != 2 {
		t.Fatalf("пойду: %d %+v", code, created)
	}
	// мест 2 — третий не влезает
	carl := register(t, h, "carl@n.io", "Карл")
	onboard(t, carl, "Карл", "m", "f")
	if code := carl.do("POST", "/api/v1/meetups/"+mid+"/join", nil, &e); code != 400 || e["error"] != "full" {
		t.Fatalf("переполнение: %d %v", code, e)
	}
	boris.do("GET", "/api/v1/meetups/"+mid, nil, &view)
	if len(view.People) != 1 || view.People[0]["name"] != "Анна" || view.People[0]["myVote"].(float64) != -1 {
		t.Fatalf("участники: %+v", view)
	}
	// «Я здесь»: за час до начала можно (начало через 30 минут)
	if code := boris.do("POST", "/api/v1/meetups/"+mid+"/checkin", nil, &created); code != 200 {
		t.Fatalf("checkin: %d %+v", code, created)
	}
	if code := carl.do("POST", "/api/v1/meetups/"+mid+"/checkin", nil, &e); code != 400 || e["error"] != "not_going" {
		t.Fatalf("checkin без «пойду»: %d %v", code, e)
	}
	anna.do("GET", "/api/v1/meetups/"+mid, nil, &view)
	if len(view.People) != 1 || view.People[0]["checkedIn"] != true {
		t.Fatalf("отметка не видна: %+v", view)
	}
	// в анкете Бориса у Анны — общее событие
	var prof map[string]any
	anna.do("GET", "/api/v1/users/"+itoa(bme.Id), nil, &prof)
	if ms := prof["meetups"].([]any); len(ms) != 1 || ms[0].(map[string]any)["both"] != true {
		t.Fatalf("события в анкете: %v", prof["meetups"])
	}
	if code := anna.do("DELETE", "/api/v1/meetups/"+mid+"/join", nil, &e); code != 400 || e["error"] != "host" {
		t.Fatalf("организатор не может уйти: %d %v", code, e)
	}
	if code := boris.do("DELETE", "/api/v1/meetups/"+mid, nil, &e); code != 403 {
		t.Fatalf("удаление не организатором: %d", code)
	}
	if code := anna.do("DELETE", "/api/v1/meetups/"+mid, nil, &okr); code != 200 || !okr["ok"] {
		t.Fatalf("отмена: %d %v", code, okr)
	}
	if code := boris.do("GET", "/api/v1/meetups/"+mid, nil, &e); code != 404 {
		t.Fatalf("после отмены: %d", code)
	}

	// ---- push: у Бориса нет открытой ленты → лайк Анны уходит пушем через очередь ----
	var vote map[string]any
	anna.do("POST", "/api/v1/votes", map[string]any{"target": bme.Id, "liked": true}, &vote)
	for i := 0; i < 50; i++ { // hub зовёт Offline в горутине — ждём, пока задача ляжет в очередь
		if n, _ := app.Queue.Size(""); n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := app.Queue.RunOnce(context.Background(), "", 10); n != 1 {
		t.Fatalf("задач в очереди выполнено: %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || len(got[0]) != 2 || got[0][0].Body != "Кому-то понравилась ваша анкета" || got[0][0].Data["url"] != "/likes" {
		t.Fatalf("пуш: %+v", got)
	}
	if n := app.Db.CreateCommand("SELECT COUNT(*) FROM {{push_tokens}}").MustInt(); n != 1 { // dead удалён
		t.Fatalf("мёртвый токен не удалён: %d", n)
	}
	_ = queue.P{}
}
