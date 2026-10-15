package main

// Тест API v1 на уровне HTTP (перенос api_v1_test.go и authflow_test.go старого бэкенда): приложение поднимается
// в процессе на SQLite, Neuronet подменяется httptest-сервером по договору Neuronet/API_AUTH.md.
// Это главный контроль совместимости с мобильным приложением: пути, коды и JSON-ключи — те же, что раньше.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"badoo/config"
	"badoo/migrations"

	"whales.team/go1"
	"whales.team/go1/app"
	"whales.team/go1/db"
	_ "whales.team/go1/db/drivers/sqlite" // тесты — на SQLite, прод — MySQL (main.go)
	"whales.team/go1/modules/admin"
	"whales.team/go1/modules/files"
	"whales.team/go1/testutil"
	"whales.team/go1/web"
)

// ---------- Поддельный Neuronet: проверяет подпись, аккаунты в памяти, код из «письма» всегда 123456 ----------

type fakeNeuronet struct {
	mu       sync.Mutex
	secret   string
	accounts map[string]string // email → пароль
	uids     map[string]int
	sent     map[string]int
	lastIP   string
}

func (f *fakeNeuronet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256(append(body, []byte(f.secret)...))
	if r.Method != "PUT" || r.Header.Get("x-neuronet-signature") != hex.EncodeToString(sum[:]) {
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Сервер временно недоступен."})
		return
	}
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastIP, _ = in["ip"].(string)
	email, _ := in["email"].(string)
	ok := func(m map[string]any) { m["status"] = 200; _ = json.NewEncoder(w).Encode(m) }
	fail := func(k, msg string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 500, "errors": map[string]string{k: msg}})
	}
	profile := func() map[string]any {
		return map[string]any{"uid": f.uids[email], "email": email, "first_name": "Нейро"}
	}
	switch strings.TrimPrefix(r.URL.Path, "/") {
	case "check-email":
		_, ex := f.accounts[email]
		ok(map[string]any{"email": email, "exists": ex, "require_invite_code": 0, "require_email_code": 1})
	case "password-login":
		if f.accounts[email] != in["password"] {
			fail("login_pass", "Неверный пароль.")
			return
		}
		ok(profile())
	case "send-code":
		f.sent[email]++
		p := 2
		if _, ex := f.accounts[email]; ex {
			p = 1
		}
		ok(map[string]any{"purpose": p, "resend_delay": 60})
	case "verify-code":
		if in["code"] != "123456" {
			fail("message", "Неверный код. Осталось попыток: 4.")
			return
		}
		if _, ex := f.accounts[email]; ex {
			m := profile()
			m["purpose"] = 1
			ok(m)
			return
		}
		ok(map[string]any{"purpose": 2, "ticket": "T-" + email})
	case "register":
		if in["ticket"] != "T-"+email {
			fail("ticket", "Подтверждение почты устарело.")
			return
		}
		f.accounts[email] = in["pass"].(string)
		f.uids[email] = 1000 + len(f.uids)
		ok(profile())
	default:
		w.WriteHeader(404)
	}
}

// ---------- Приложение в процессе ----------

func newTestApp(t *testing.T, nnURL string) http.Handler {
	t.Helper()
	cfg := config.App
	cfg.Env = "test"
	cfg.BasePath = t.TempDir()
	cfg.Db = testutil.Config(t)
	cfg.Redis = ""
	cfg.Metrics.Enabled = false
	cfg.Migrations = migrations.All()
	cfg.Modules = []go1.Module{
		admin.New(admin.Config{Title: "Amura", Role: "admin"}),
		files.New(files.Config{Storage: files.Local{Dir: t.TempDir(), BaseUrl: "/uploads"}}),
	}
	cfg.Session = web.SessionConfig{Key: []byte("0123456789abcdef0123456789abcdef")}
	p := map[string]any{}
	for k, v := range config.App.Params {
		p[k] = v
	}
	p["neuronet.url"], p["neuronet.service"], p["neuronet.secret"] = nnURL, 42, "s3cr3t"
	p["amura.dev"], p["amura.chat_verify"], p["push.enabled"] = true, false, false
	cfg.Params = p
	a := go1.New(cfg)
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	web.SetRateLimitStore(web.NewMemoryRateLimit())
	if _, err := db.NewMigrator(app.Db, a.AllMigrations()).Up(0); err != nil {
		t.Fatal(err)
	}
	return a.Server.Handler()
}

// client — как src/lib/api.ts: JSON, Bearer, разбор ответа.
type client struct {
	t     *testing.T
	h     http.Handler
	token string
}

func (c *client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, "http://badoo.test"+path, rd)
	r.RemoteAddr = "203.0.113.7:1234"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	if out != nil && w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			c.t.Fatalf("%s %s: не JSON (%d): %s", method, path, w.Code, w.Body.String())
		}
	}
	return w.Code
}

func (c *client) upload(path, field string, out any) int {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, "a.png")
	img := image.NewRGBA(image.Rect(0, 0, 40, 60))
	for i := range img.Pix {
		img.Pix[i] = 0xC0
	}
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	_ = png.Encode(fw, img)
	_ = mw.Close()
	r := httptest.NewRequest("POST", "http://badoo.test"+path, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+c.token)
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	if out != nil {
		_ = json.Unmarshal(w.Body.Bytes(), out)
	}
	return w.Code
}

type meResp struct {
	Id           int64    `json:"id"`
	Name         string   `json:"name"`
	Onboarded    bool     `json:"onboarded"`
	Premium      bool     `json:"premium"`
	Verification int      `json:"verification"`
	Invisible    bool     `json:"invisible"`
	MaxKm        int      `json:"maxKm"`
	Photos       []string `json:"photos"`
	PhotoList    []struct {
		Id  int64  `json:"id"`
		Url string `json:"url"`
	} `json:"photoList"`
	Color int `json:"color"`
}

type tokResp struct {
	Token string `json:"token"`
	Me    meResp `json:"me"`
}

type flowResp struct {
	Flow, Step, Email string
	ResendDelay       int `json:"resendDelay"`
}

// register — новый пользователь через Neuronet: email → код → регистрация; возвращает клиента с токеном.
func register(t *testing.T, h http.Handler, email, name string) *client {
	t.Helper()
	c := &client{t: t, h: h}
	var f flowResp
	if code := c.do("POST", "/api/v1/auth/email", map[string]string{"email": email}, &f); code != 200 || f.Step != "code" {
		t.Fatalf("email %s: %d %+v", email, code, f)
	}
	var f2 flowResp
	c.do("POST", "/api/v1/auth/verify-code", map[string]string{"flow": f.Flow, "code": "123456"}, &f2)
	if f2.Step != "register" {
		t.Fatalf("после кода ждали регистрацию: %+v", f2)
	}
	var tr tokResp
	if code := c.do("POST", "/api/v1/auth/register", map[string]string{"flow": f.Flow, "name": name, "pass": "newpass99", "pass2": "newpass99"}, &tr); code != 200 || tr.Token == "" {
		t.Fatalf("регистрация: %d %+v", code, tr)
	}
	c.token = tr.Token
	return c
}

func onboard(t *testing.T, c *client, name, gender, seeking string) meResp {
	t.Helper()
	var me meResp
	in := map[string]any{"name": name, "birthdate": "1992-03-04", "gender": gender, "seeking": seeking, "city": "Мадрид",
		"lat": 40.4168, "lng": -3.7038, "bio": "Привет", "intent": "serious", "height": 175, "interests": []string{"Путешествия"}}
	if code := c.do("PUT", "/api/v1/me", in, &me); code != 200 || !me.Onboarded || me.Name != name {
		t.Fatalf("онбординг %s: %d %+v", name, code, me)
	}
	return me
}

func TestApiV1(t *testing.T) {
	fake := &fakeNeuronet{secret: "s3cr3t", accounts: map[string]string{"old@n.io": "oldpass99"}, uids: map[string]int{"old@n.io": 777}, sent: map[string]int{}}
	nsrv := httptest.NewServer(fake)
	defer nsrv.Close()
	h := newTestApp(t, nsrv.URL)
	guest := &client{t: t, h: h}

	// ---- meta и доступ без токена ----
	var meta map[string]any
	if code := guest.do("GET", "/api/v1/meta", nil, &meta); code != 200 || meta["dev"] != true || len(meta["cities"].([]any)) == 0 {
		t.Fatalf("meta: %d %v", code, meta)
	}
	var e map[string]string
	if code := guest.do("GET", "/api/v1/me", nil, &e); code != 401 || e["error"] != "unauthorized" {
		t.Fatalf("без токена: %d %v", code, e)
	}
	if code := guest.do("POST", "/api/v1/auth/login", map[string]string{"email": "x@y.z", "password": "p"}, &e); code != 410 {
		t.Fatalf("старый вход: %d %v", code, e)
	}

	// ---- вход через Neuronet: новый, с ошибками по пути ----
	var f flowResp
	guest.do("POST", "/api/v1/auth/email", map[string]string{"email": "New@N.io"}, &f)
	if f.Step != "code" || fake.sent["new@n.io"] != 1 || f.ResendDelay == 0 {
		t.Fatalf("код не ушёл автоматически: %+v %v", f, fake.sent)
	}
	if code := guest.do("POST", "/api/v1/auth/verify-code", map[string]string{"flow": f.Flow, "code": "000000"}, &e); code != 400 || e["error"] != "code" {
		t.Fatalf("неверный код: %d %v", code, e)
	}
	guest.do("POST", "/api/v1/auth/verify-code", map[string]string{"flow": f.Flow, "code": "123456"}, &f)
	if code := guest.do("POST", "/api/v1/auth/register", map[string]string{"flow": f.Flow, "name": "Ника", "pass": "newpass99", "pass2": "nope"}, &e); code != 400 || e["error"] != "pass2" {
		t.Fatalf("пароли не совпали: %d %v", code, e)
	}
	var tr tokResp
	if code := guest.do("POST", "/api/v1/auth/register", map[string]string{"flow": f.Flow, "name": "Ника", "pass": "newpass99", "pass2": "newpass99"}, &tr); code != 200 || tr.Token == "" || tr.Me.Onboarded {
		t.Fatalf("регистрация: %d %+v", code, tr)
	}
	if n := app.Db.CreateCommand("SELECT neuronet_uid FROM {{users}} WHERE email = 'new@n.io'").MustInt(); n == 0 {
		t.Fatal("neuronet_uid не сохранён")
	}
	if code := guest.do("POST", "/api/v1/auth/register", map[string]string{"flow": f.Flow, "name": "x", "pass": "newpass99", "pass2": "newpass99"}, nil); code != 410 {
		t.Fatalf("повтор попытки = %d", code)
	}
	if fake.lastIP != "203.0.113.7" {
		t.Fatalf("в Neuronet не ушёл ip пользователя: %q", fake.lastIP)
	}
	anna := &client{t: t, h: h, token: tr.Token}

	// ---- существующий в Neuronet + уже есть локальная анкета с той же почтой — привязывается ----
	app.Db.MustInsert("{{users}}", db.H{"email": "old@n.io", "pass_hash": "x", "name": "Старый", "onboarded": true, "birthdate": "1990-01-01",
		"gender": "m", "seeking": "f", "city": "Мадрид", "lat": 40.41, "lng": -3.70, "last_seen": time.Now().UTC(), "created_at": time.Now().UTC()})
	guest.do("POST", "/api/v1/auth/email", map[string]string{"email": "old@n.io"}, &f)
	if f.Step != "password" {
		t.Fatalf("ждали пароль: %+v", f)
	}
	if code := guest.do("POST", "/api/v1/auth/password", map[string]string{"flow": f.Flow, "password": "bad"}, &e); code != 400 || e["error"] != "password" {
		t.Fatalf("неверный пароль: %d %v", code, e)
	}
	guest.do("POST", "/api/v1/auth/password", map[string]string{"flow": f.Flow, "password": "oldpass99"}, &tr)
	if tr.Me.Name != "Старый" || !tr.Me.Onboarded {
		t.Fatalf("не привязался к существующей анкете: %+v", tr.Me)
	}
	// вход по коду для существующего
	guest.do("POST", "/api/v1/auth/email", map[string]string{"email": "old@n.io"}, &f)
	guest.do("POST", "/api/v1/auth/send-code", map[string]string{"flow": f.Flow}, nil)
	tr = tokResp{}
	guest.do("POST", "/api/v1/auth/verify-code", map[string]string{"flow": f.Flow, "code": "123456"}, &tr)
	if tr.Token == "" || tr.Me.Name != "Старый" {
		t.Fatalf("вход по коду: %+v", tr)
	}

	// ---- анкета: без онбординга 403, PUT /me, фото, настройки ----
	if code := anna.do("GET", "/api/v1/encounters", nil, &e); code != 403 || e["error"] != "onboarding" {
		t.Fatalf("до онбординга: %d %v", code, e)
	}
	if code := anna.do("PUT", "/api/v1/me", map[string]any{"name": "A"}, &e); code != 400 || e["error"] != "validation" {
		t.Fatalf("валидация анкеты: %d %v", code, e)
	}
	me := onboard(t, anna, "Анна", "f", "m")
	var me2 meResp
	if code := anna.upload("/api/v1/me/photos", "photo", &me2); code != 200 || len(me2.PhotoList) != 1 || !strings.Contains(me2.Photos[0], "/uploads/") {
		t.Fatalf("фото: %d %+v", code, me2)
	}
	if code := anna.do("PUT", "/api/v1/me/settings", map[string]any{"ageMin": 20, "ageMax": 40, "maxKm": 500, "seeking": "m", "invisible": true}, &e); code != 402 || e["error"] != "premium" {
		t.Fatalf("невидимка без Premium: %d %v", code, e)
	}
	if code := anna.do("PUT", "/api/v1/me/settings", map[string]any{"ageMin": 20, "ageMax": 40, "maxKm": 500, "seeking": "m"}, &me2); code != 200 || me2.MaxKm != 300 {
		t.Fatalf("настройки: %d %+v", code, me2)
	}

	boris := register(t, h, "boris@n.io", "Борис")
	onboard(t, boris, "Борис", "m", "f")
	var okr map[string]bool
	if code := boris.do("POST", "/api/v1/me/push", map[string]string{"token": "ExponentPushToken[abc]", "platform": "ios"}, &okr); code != 200 || !okr["ok"] {
		t.Fatalf("push: %d %v", code, okr)
	}

	// ---- знакомства: колода, визит, лайк → мэтч, события ----
	var enc struct {
		Cards   []meResp `json:"cards"`
		Premium bool     `json:"premium"`
	}
	if code := boris.do("GET", "/api/v1/encounters", nil, &enc); code != 200 || len(enc.Cards) != 1 || enc.Cards[0].Id != me.Id {
		t.Fatalf("колода Бориса: %d %+v", code, enc)
	}
	var seqResp struct {
		Seq    int64 `json:"seq"`
		Events []any `json:"events"`
	}
	anna.do("GET", "/api/v1/events", nil, &seqResp)
	seq0 := seqResp.Seq

	var prof map[string]any
	if code := anna.do("GET", "/api/v1/users/"+itoa(boris.me(t).Id), nil, &prof); code != 200 || prof["myVote"].(float64) != -1 || prof["isMe"] != false {
		t.Fatalf("анкета Бориса: %d %v", code, prof)
	}
	var vote map[string]any
	anna.do("POST", "/api/v1/votes", map[string]any{"target": boris.me(t).Id, "liked": true}, &vote)
	if vote["match"] != false {
		t.Fatalf("первый лайк — ещё не мэтч: %v", vote)
	}
	var likes map[string]any
	boris.do("GET", "/api/v1/likes", nil, &likes)
	if likes["likesCount"].(float64) != 1 || len(likes["likers"].([]any)) != 0 || len(likes["lockedColors"].([]any)) != 1 {
		t.Fatalf("симпатии без Premium: %v", likes)
	}
	var vis map[string]any
	boris.do("GET", "/api/v1/visitors", nil, &vis)
	if len(vis["visitors"].([]any)) != 1 {
		t.Fatalf("гости: %v", vis)
	}
	boris.do("POST", "/api/v1/votes", map[string]any{"target": me.Id, "liked": true}, &vote)
	if vote["match"] != true || vote["matchId"].(float64) == 0 || vote["name"] != "Анна" {
		t.Fatalf("мэтч: %v", vote)
	}
	mid := itoa(int64(vote["matchId"].(float64)))
	anna.do("GET", "/api/v1/events?since="+itoa(seq0), nil, &seqResp)
	if len(seqResp.Events) != 1 || seqResp.Events[0].(map[string]any)["type"] != "match" {
		t.Fatalf("события Анны: %+v", seqResp)
	}
	boris.do("GET", "/api/v1/events?since=0", nil, &seqResp)
	if len(seqResp.Events) != 1 || seqResp.Events[0].(map[string]any)["type"] != "like" { // лайк Анны пришёл Борису
		t.Fatalf("события Бориса: %+v", seqResp)
	}

	// ---- чат ----
	var chats map[string]any
	anna.do("GET", "/api/v1/chats", nil, &chats)
	if len(chats["chats"].([]any)) != 1 {
		t.Fatalf("чаты: %v", chats)
	}
	var msg map[string]any
	if code := boris.do("POST", "/api/v1/chats/"+mid+"/messages", map[string]string{"body": "Привет!"}, &msg); code != 200 || msg["mine"] != true {
		t.Fatalf("отправка: %d %v", code, msg)
	}
	var counts map[string]int
	anna.do("GET", "/api/v1/counts", nil, &counts)
	if counts["unread"] != 1 {
		t.Fatalf("непрочитанные: %v", counts)
	}
	var chat map[string]any
	if code := anna.do("GET", "/api/v1/chats/"+mid, nil, &chat); code != 200 || len(chat["messages"].([]any)) != 1 || chat["canSend"] != true {
		t.Fatalf("чат: %d %v", code, chat)
	}
	anna.do("GET", "/api/v1/counts", nil, &counts)
	if counts["unread"] != 0 {
		t.Fatalf("после открытия чата: %v", counts)
	}
	var msgs []map[string]any
	boris.do("GET", "/api/v1/chats/"+mid+"/messages?after=0", nil, &msgs)
	if len(msgs) != 1 || msgs[0]["read"] != true {
		t.Fatalf("прочитано не дошло: %v", msgs)
	}
	if code := guest.do("GET", "/api/v1/chats/"+mid, nil, &e); code != 401 {
		t.Fatalf("чужой чат без токена: %d", code)
	}

	// ---- Premium: undo закрыт, активация в dev, отмена ----
	if code := boris.do("POST", "/api/v1/undo", nil, &e); code != 402 || e["error"] != "premium" {
		t.Fatalf("undo без Premium: %d %v", code, e)
	}
	var prem map[string]any
	boris.do("GET", "/api/v1/premium", nil, &prem)
	if prem["active"] != false || len(prem["plans"].([]any)) != 3 {
		t.Fatalf("premium: %v", prem)
	}
	if code := boris.do("POST", "/api/v1/premium", map[string]string{"plan": "month"}, &me2); code != 200 || !me2.Premium {
		t.Fatalf("активация: %d %+v", code, me2)
	}
	if code := boris.do("DELETE", "/api/v1/premium", nil, &me2); code != 200 || me2.Premium {
		t.Fatalf("отмена: %d %+v", code, me2)
	}

	// ---- верификация (dev — сразу), жалоба с блокировкой, выход ----
	if code := anna.upload("/api/v1/me/verify", "selfie", &me2); code != 200 || me2.Verification != 2 {
		t.Fatalf("верификация: %d %+v", code, me2)
	}
	if code := anna.do("POST", "/api/v1/users/"+itoa(boris.me(t).Id)+"/report", map[string]any{"reason": "Спам или реклама", "block": true}, &okr); code != 200 {
		t.Fatalf("жалоба: %d %v", code, okr)
	}
	if code := boris.do("GET", "/api/v1/users/"+itoa(me.Id), nil, &e); code != 404 {
		t.Fatalf("после блокировки анкета видна: %d", code)
	}
	if code := boris.do("GET", "/api/v1/chats/"+mid, nil, &e); code != 404 {
		t.Fatalf("после блокировки чат виден: %d", code)
	}
	if code := anna.do("POST", "/api/v1/auth/logout", nil, &okr); code != 200 {
		t.Fatalf("logout: %d %v", code, okr)
	}
	if code := anna.do("GET", "/api/v1/me", nil, &e); code != 401 {
		t.Fatalf("токен после выхода жив: %d", code)
	}
	if code := boris.do("DELETE", "/api/v1/me", map[string]string{"confirm": "УДАЛИТЬ"}, &okr); code != 200 {
		t.Fatalf("удаление: %d %v", code, okr)
	}
	if n := app.Db.CreateCommand("SELECT COUNT(*) FROM {{users}}").MustInt(); n != 2 { // Анна и «Старый»
		t.Fatalf("после удаления users = %d", n)
	}
}

func (c *client) me(t *testing.T) meResp {
	t.Helper()
	var m meResp
	if code := c.do("GET", "/api/v1/me", nil, &m); code != 200 {
		t.Fatalf("me: %d", code)
	}
	return m
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
