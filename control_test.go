package main

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"badoo/modules/control"

	"whales.team/go1/app"
)

// browser — вход на сайт как в браузере: cookie-сессия Go1 + CSRF-токен со страницы (админка работает только так).
type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]*http.Cookie
	csrf    string
}

var csrfRe = regexp.MustCompile(`name="(?:csrf-token|_csrf)"\s+(?:content|value)="([^"]+)"`)

func (b *browser) req(method, path string, form url.Values, ajax bool) (int, string, http.Header) {
	b.t.Helper()
	body := ""
	if form != nil {
		if ajax {
			body = form.Encode()
		} else {
			f := url.Values{}
			for k, v := range form {
				f[k] = v
			}
			f.Set("_csrf", b.csrf)
			body = f.Encode()
		}
	}
	r := httptest.NewRequest(method, "http://badoo.test"+path, strings.NewReader(body))
	r.RemoteAddr = "203.0.113.9:1234"
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if ajax {
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		r.Header.Set("X-CSRF-Token", b.csrf)
	}
	for _, c := range b.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	out := w.Body.String()
	if m := csrfRe.FindStringSubmatch(out); m != nil {
		b.csrf = html.UnescapeString(m[1])
	}
	return w.Code, out, w.Header()
}

// login — /login: почта → пароль (аккаунт уже есть в поддельном Neuronet).
func webLogin(t *testing.T, h http.Handler, email, pass string) *browser {
	t.Helper()
	b := &browser{t: t, h: h, cookies: map[string]*http.Cookie{}}
	b.req("GET", "/login", nil, false)
	if b.csrf == "" {
		t.Fatal("на /login нет CSRF-токена")
	}
	b.req("POST", "/login/email", url.Values{"email": {email}}, false)
	b.req("GET", "/login", nil, false)
	if code, body, hd := b.req("POST", "/login/password", url.Values{"password": {pass}}, false); code != 302 && code != 303 {
		t.Fatalf("вход %s: %d %s %.300s", email, code, hd.Get("Location"), body)
	}
	return b
}

// get — страница админки
func (b *browser) get(path string) (int, string) {
	code, body, _ := b.req("GET", path, nil, false)
	return code, body
}

// call — POST в экшен админки (AJAX), ответ JSON
func (b *browser) call(path string, form url.Values) map[string]any {
	b.t.Helper()
	_, body, _ := b.req("POST", path, form, true)
	var out map[string]any
	_ = json.Unmarshal([]byte(body), &out)
	return out
}

// post — POST в экшен админки, ждём {"status":200}
func (b *browser) post(path string, form url.Values) map[string]any {
	b.t.Helper()
	out := b.call(path, form)
	if out["status"] != float64(200) {
		b.t.Fatalf("POST %s: %+v", path, out)
	}
	return out
}

func TestControl(t *testing.T) {
	fake := &fakeNeuronet{secret: "s3cr3t", accounts: map[string]string{}, uids: map[string]int{}, sent: map[string]int{}}
	nsrv := httptest.NewServer(fake)
	defer nsrv.Close()
	h := newTestApp(t, nsrv.URL)

	boss := register(t, h, "boss@x.io", "Босс")
	onboard(t, boss, "Босс", "m", "f")
	anna := register(t, h, "anna@x.io", "Анна")
	annaMe := onboard(t, anna, "Анна", "f", "m")
	bob := register(t, h, "bob@x.io", "Боб")
	bobMe := onboard(t, bob, "Боб", "m", "f")
	bossMe := boss.me(t)

	// пара, сообщение, жалоба
	anna.do("POST", "/api/v1/votes", map[string]any{"target": bobMe.Id, "liked": true}, nil)
	bob.do("POST", "/api/v1/votes", map[string]any{"target": annaMe.Id, "liked": true}, nil)
	var chats struct{ Chats []map[string]any }
	anna.do("GET", "/api/v1/chats", nil, &chats)
	if len(chats.Chats) != 1 {
		t.Fatalf("пары нет: %+v", chats)
	}
	mid := itoa(int64(chats.Chats[0]["matchId"].(float64)))
	bob.do("POST", "/api/v1/chats/"+mid+"/messages", map[string]string{"body": "пиши в телеграм @scam"}, nil)
	anna.do("POST", "/api/v1/users/"+itoa(bobMe.Id)+"/report", map[string]any{"reason": "Спам или реклама"}, nil)

	bossW := webLogin(t, h, "boss@x.io", "newpass99")
	bobW := webLogin(t, h, "bob@x.io", "newpass99")

	// без прав админки: страница — уводит на сайт, данные — 403
	if code, _ := bobW.get("/control"); code != 302 && code != 303 {
		t.Fatalf("не админ на /control: %d", code)
	}
	if out := bobW.call("/control/users/get-users", url.Values{}); out["status"] != float64(403) {
		t.Fatalf("не админ получил данные: %+v", out)
	}
	// данные — только POST
	if code, _ := bossW.get("/control/users/get-users"); code != 404 && code != 405 {
		t.Fatalf("GET экшена данных: %d", code)
	}

	if _, err := control.MakeSuper("boss@x.io"); err != nil {
		t.Fatal(err)
	}

	// все страницы открываются
	for _, p := range []string{"/control", "/control/users", "/control/users/view?uid=" + itoa(bobMe.Id), "/control/chats",
		"/control/chats/view?id=" + mid, "/control/reports", "/control/verification", "/control/photos", "/control/meetups",
		"/control/log", "/control/permissions"} {
		code, body := bossW.get(p)
		if code != 200 || !strings.Contains(body, "c-sidebar") || !strings.Contains(body, "Пользователи") {
			t.Fatalf("GET %s: %d %.300s", p, code, body)
		}
	}

	// дашборд
	info := bossW.post("/control/index/get-index-info", url.Values{})
	if u := info["users"].(map[string]any); u["total"] != float64(3) || u["onboarded"] != float64(3) {
		t.Fatalf("дашборд: %+v", u)
	}
	if a := info["activity"].(map[string]any); a["matches"] != float64(1) || a["messages"] != float64(1) || a["reports_new"] != float64(1) {
		t.Fatalf("активность: %+v", a)
	}
	if regs := info["days"].(map[string]any)["regs"].([]any); len(regs) != 1 || regs[0].(map[string]any)["n"] != float64(3) {
		t.Fatalf("регистрации по дням: %+v", regs)
	}

	// список с фильтрами
	list := bossW.post("/control/users/get-users", url.Values{"gender": {"f"}})
	if list["count"] != float64(1) || list["items"].([]any)[0].(map[string]any)["name"] != "Анна" {
		t.Fatalf("фильтр по полу: %+v", list)
	}
	list = bossW.post("/control/users/get-users", url.Values{"reported": {"1"}, "age_from": {"30"}, "age_to": {"40"}, "city": {"Мадрид"}, "seen": {"24h"}})
	if list["count"] != float64(1) || list["items"].([]any)[0].(map[string]any)["reports_count"] != float64(1) {
		t.Fatalf("фильтр с жалобами: %+v", list)
	}
	if created := list["items"].([]any)[0].(map[string]any)["created_at"].(float64); created < 1e9 {
		t.Fatalf("дата не в unix: %v", created)
	}
	for _, f := range []url.Values{{"search": {"bob@"}}, {"status": {"banned"}}, {"verified": {"2"}}, {"premium": {"1"}}, {"photos": {"0"}},
		{"created_from": {"2020-01-01"}, "created_to": {"2099-01-01"}}, {"sort": {"reports"}}, {"app": {"1"}}, {"onboarded": {"0"}}} {
		bossW.post("/control/users/get-users", f)
	}

	// карточка и вкладки
	card := bossW.post("/control/users/get-user-info", url.Values{"uid": {itoa(bobMe.Id)}})
	if card["counts"].(map[string]any)["reports_in"] != float64(1) || card["item"].(map[string]any)["email"] != "bob@x.io" {
		t.Fatalf("карточка: %+v", card)
	}
	for _, p := range []string{"get-matches", "get-votes", "get-visits", "get-reports", "get-devices", "get-meetups", "get-log"} {
		bossW.post("/control/users/"+p, url.Values{"uid": {itoa(bobMe.Id)}})
	}

	// переписка: список, поиск по тексту, диалог
	if l := bossW.post("/control/chats/get-chats", url.Values{"text": {"телеграм"}, "empty": {"0"}}); l["count"] != float64(1) {
		t.Fatalf("поиск диалога по тексту: %+v", l)
	}
	if l := bossW.post("/control/chats/search-messages", url.Values{"text": {"scam"}}); l["count"] != float64(1) {
		t.Fatalf("поиск сообщений: %+v", l)
	}
	msgs := bossW.post("/control/chats/get-messages", url.Values{"id": {mid}})
	items := msgs["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("диалог: %+v", msgs)
	}

	// жалобы
	reps := bossW.post("/control/reports/get-reports", url.Values{"status": {"0"}})
	if reps["count"] != float64(1) {
		t.Fatalf("жалобы: %+v", reps)
	}
	repId := itoa(int64(reps["items"].([]any)[0].(map[string]any)["id"].(float64)))

	// себя и суперадмина не заблокировать
	if out := bossW.call("/control/users/set-ban", url.Values{"uid": {itoa(bossMe.Id)}, "ban": {"1"}}); out["status"] == float64(200) {
		t.Fatal("заблокировал сам себя")
	}

	// блокировка по жалобе: Боб пропал из выдачи, приложение разлогинено, войти нельзя
	bossW.post("/control/reports/resolve", url.Values{"id": {repId}, "action": {"ban"}, "reason": {"спам"}})
	if code := bob.do("GET", "/api/v1/me", nil, nil); code != 401 {
		t.Fatalf("заблокированный в приложении: %d", code)
	}
	if code, _ := bobW.get("/encounters"); code != 302 && code != 303 {
		t.Fatalf("заблокированный на сайте: %d", code)
	}
	chats.Chats = nil
	anna.do("GET", "/api/v1/chats", nil, &chats)
	if len(chats.Chats) != 0 {
		t.Fatalf("чат с заблокированным виден: %+v", chats)
	}
	guest := &client{t: t, h: h}
	var f flowResp
	guest.do("POST", "/api/v1/auth/email", map[string]string{"email": "bob@x.io"}, &f)
	var e map[string]any
	code := guest.do("POST", "/api/v1/auth/password", map[string]string{"flow": f.Flow, "password": "newpass99"}, &e)
	if code == 200 || !strings.Contains(e["message"].(string), "заблокирован") || !strings.Contains(e["message"].(string), "спам") {
		t.Fatalf("заблокированный вошёл: %d %+v", code, e)
	}
	if n := app.Db.CreateCommand("SELECT COUNT(*) FROM {{reports}} WHERE status = 1").MustInt(); n != 1 {
		t.Fatalf("жалоба не закрыта: %d", n)
	}
	if l := bossW.post("/control/users/get-users", url.Values{"status": {"banned"}}); l["count"] != float64(1) {
		t.Fatalf("фильтр заблокированных: %+v", l)
	}

	// разблокировка — снова входит
	bossW.post("/control/users/set-ban", url.Values{"uid": {itoa(bobMe.Id)}, "ban": {"0"}})
	var tr tokResp
	guest.do("POST", "/api/v1/auth/email", map[string]string{"email": "bob@x.io"}, &f)
	if code := guest.do("POST", "/api/v1/auth/password", map[string]string{"flow": f.Flow, "password": "newpass99"}, &tr); code != 200 || tr.Token == "" {
		t.Fatalf("после разблокировки не вошёл: %d", code)
	}

	// удаление сообщения — шапка пары пересчитана
	msgId := itoa(int64(items[0].(map[string]any)["id"].(float64)))
	bossW.post("/control/chats/delete-message", url.Values{"id": {msgId}})
	if n := app.Db.CreateCommand("SELECT COUNT(*) FROM {{matches}} WHERE last_msg_id IS NULL").MustInt(); n != 1 {
		t.Fatalf("last_msg_id не пересчитан: %d", n)
	}

	// верификация, Premium, правка, выход везде
	app.Db.CreateCommand("UPDATE {{users}} SET verified = 1, verify_file = 'private/x.jpg' WHERE id = :id").BindValue(":id", annaMe.Id).MustExecute()
	if q := bossW.post("/control/verification/get-queue", url.Values{}); q["count"] != float64(1) {
		t.Fatalf("очередь селфи: %+v", q)
	}
	bossW.post("/control/verification/decide", url.Values{"uid": {itoa(annaMe.Id)}, "verified": {"2"}})
	if anna.me(t).Verification != 2 {
		t.Fatal("не подтверждена")
	}
	bossW.post("/control/users/set-premium", url.Values{"uid": {itoa(annaMe.Id)}, "days": {"30"}})
	if !anna.me(t).Premium {
		t.Fatal("Premium не начислен")
	}
	bossW.post("/control/users/edit-user-info", url.Values{"uid": {itoa(annaMe.Id)}, "name": {"Анна К."}, "bio": {""}, "city": {"Валенсия"}})
	if me := anna.me(t); me.Name != "Анна К." {
		t.Fatalf("правка: %+v", me)
	}
	bossW.post("/control/users/logout", url.Values{"uid": {itoa(annaMe.Id)}})
	if code := anna.do("GET", "/api/v1/me", nil, nil); code != 401 {
		t.Fatalf("после «выйти везде»: %d", code)
	}

	// прочие разделы
	bossW.post("/control/photos/get-photos", url.Values{})
	bossW.post("/control/meetups/get-meetups", url.Values{"when": {"all"}})
	if l := bossW.post("/control/log/get-log", url.Values{}); l["count"].(float64) < 6 {
		t.Fatalf("журнал: %+v", l)
	}

	// права: дать Анне «Пользователи» — откроется только этот раздел
	perms := bossW.post("/control/permissions/get-user-permissions", url.Values{"uid": {itoa(bobMe.Id)}})
	var usersSection string
	for _, it := range perms["items"].([]any) {
		if m := it.(map[string]any); m["action"] == "users" {
			usersSection = itoa(int64(m["id"].(float64)))
		}
	}
	bossW.post("/control/permissions/change-status", url.Values{"uid": {itoa(bobMe.Id)}, "id": {usersSection}, "status": {"1"}})
	bobW = webLogin(t, h, "bob@x.io", "newpass99") // после блокировки его cookie-сессия сброшена
	if code, _ := bobW.get("/control/users"); code != 200 {
		t.Fatalf("раздел с правом: %d", code)
	}
	if code, _ := bobW.get("/control/chats"); code != 302 && code != 303 {
		t.Fatalf("раздел без права: %d", code)
	}
	// админ без суперправ не блокирует суперадмина
	if out := bobW.call("/control/users/set-ban", url.Values{"uid": {itoa(bossMe.Id)}, "ban": {"1"}}); out["status"] == float64(200) {
		t.Fatal("админ заблокировал суперадмина")
	}
	bossW.post("/control/permissions/get-sections", url.Values{})
	bossW.post("/control/permissions/get-users", url.Values{})
	bossW.post("/control/permissions/get-history", url.Values{"uid": {itoa(bobMe.Id)}})
	bossW.post("/control/permissions/save-section", url.Values{"action": {"stats"}, "name": {"Статистика"}, "cid": {"2"}})

	// удаление аккаунта (суперадмин)
	bossW.post("/control/users/delete-user", url.Values{"uid": {itoa(annaMe.Id)}})
	if n := app.Db.CreateCommand("SELECT COUNT(*) FROM {{users}} WHERE id = :id").BindValue(":id", annaMe.Id).MustInt(); n != 0 {
		t.Fatal("аккаунт не удалён")
	}
}
