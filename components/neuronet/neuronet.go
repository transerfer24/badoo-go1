// Package neuronet — пошаговый вход через Neuronet (перенос backend/authflow.go).
//
// Тот же сценарий, что в приложении «Я есть» (Iam/WealthBook) и на neuronet.app/signin:
//
//  1. email           → check-email   → пароль (аккаунт есть) | код (новый) | регистрация
//     2A. пароль         → password-login → вход
//     «войти по коду» → send-code
//     2B. код из письма  → verify-code   → вход (аккаунт был) | регистрация (почта подтверждена)
//  3. имя, код приглашения, пароль ×2 → register → вход
//
// Аккаунт живёт в Neuronet: Amura не хранит его пароль, а заводит у себя запись, привязанную к neuronet_uid.
// Подпись запросов — секрет сервиса, поэтому в Neuronet ходит только сервер.
//
// Своей регистрации и своих паролей у Amura нет: аккаунты только в Neuronet (как у wealthbook.app/signin и
// приложения «Я есть»). Без NEURONET_SERVICE/NEURONET_SECRET вход не работает и честно об этом говорит.
// Колонка users.pass_hash осталась от старого бэкенда (локальные тестовые анкеты) и больше не используется.
//
// Транспорт (страницы и JSON) — в контроллерах; здесь только шаги и попытка входа (auth_flows).
package neuronet

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

// Client — подписанные запросы к Neuronet. nil — Neuronet не настроен.
type Client struct {
	url     string
	service int
	secret  string
	http    *http.Client
}

// New — клиент из app.Params (neuronet.url / neuronet.service / neuronet.secret), nil без сервиса и секрета.
func New() *Client {
	sid, _ := app.Params["neuronet.service"].(int)
	secret, _ := app.Params["neuronet.secret"].(string)
	if sid == 0 || secret == "" {
		return nil
	}
	url, _ := app.Params["neuronet.url"].(string)
	return &Client{
		url:     strings.TrimRight(url, "/"),
		service: sid, secret: secret,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// Enabled — вход идёт через Neuronet (иначе — локальные аккаунты).
func (n *Client) Enabled() bool { return n != nil }

// Err — ошибка шага с полем, под которым её показать.
// Поля: email, password, code, name, invite, pass, pass2, ticket, message, flow.
type Err struct {
	Field string
	Msg   string
}

func (e *Err) Error() string { return e.Msg }

func aerr(field, msg string) *Err { return &Err{field, msg} }

// Ключи ошибок Neuronet → поля наших экранов.
var neuronetFields = map[string]string{
	"login_email": "email", "login_pass": "password", "reg_name": "name", "reg_pass": "pass",
	"reg_pass_2": "pass2", "utm_source": "invite", "ticket": "ticket", "reg_errors": "message", "message": "message",
}

const unavailable = "Сервер авторизации временно недоступен. Попробуйте через минуту."
const notConfigured = "Вход не настроен: задайте NEURONET_SERVICE и NEURONET_SECRET в .env."

// Call — подписанный PUT <url>/<action>; ip — адрес пользователя (Neuronet считает лимиты по нему).
func (n *Client) Call(action string, data map[string]any, ip string) (map[string]any, *Err) {
	data["service"] = n.service
	data["ip"] = ip
	body, _ := json.Marshal(data)
	sum := sha256.Sum256(append(append([]byte{}, body...), []byte(n.secret)...))
	req, _ := http.NewRequest("PUT", n.url+"/"+action, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-neuronet-signature", hex.EncodeToString(sum[:]))
	res, err := n.http.Do(req)
	if err != nil {
		app.Log.Error("neuronet " + action + ": " + err.Error())
		return nil, aerr("message", unavailable)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		app.Log.Error(fmt.Sprintf("neuronet %s: не JSON (%d): %.200s", action, res.StatusCode, raw))
		return nil, aerr("message", unavailable)
	}
	if st, _ := out["status"].(float64); st == 200 {
		return out, nil
	}
	// Отказ по подписи/сервису выглядит как {"message": "..."} без status.
	if errs, ok := out["errors"].(map[string]any); ok {
		for k, v := range errs {
			f := neuronetFields[k]
			if f == "" {
				f = "message"
			}
			return nil, aerr(f, fmt.Sprint(v))
		}
	}
	if m, ok := out["message"].(string); ok && m != "" {
		app.Log.Error("neuronet " + action + ": отказ без полей: " + m + " (проверьте NEURONET_SERVICE/SECRET и ip сервиса)")
		return nil, aerr("message", m)
	}
	return nil, aerr("message", "Сервер авторизации временно недоступен.")
}

// ---------- Попытка входа (хранится на сервере) ----------

// Flow — попытка входа. Email и тикет Neuronet живут здесь, клиент носит только id:
// иначе код, отправленный на один адрес, можно было бы подтвердить для другого.
type Flow struct {
	Id            string
	Email         string
	Step          string // password | code | register
	Ticket        string
	RequireInvite bool
	ResendAt      int64
}

const FlowTTL = 30 * time.Minute

// ResendDelay — сколько секунд ждать до повторного письма (0 — можно сейчас).
func (f *Flow) ResendDelay() int {
	if w := f.ResendAt - time.Now().Unix(); w > 0 {
		return int(w)
	}
	return 0
}

func FlowGet(id string) (*Flow, *Err) {
	row := app.Db.CreateCommand("SELECT * FROM {{auth_flows}} WHERE id = :id").BindValue(":id", id).MustRow()
	if row == nil || row.Int64("expires_at") < time.Now().Unix() {
		return nil, aerr("flow", "Попытка входа устарела — начните с почты заново.")
	}
	return &Flow{
		Id: row.Str("id"), Email: row.Str("email"), Step: row.Str("step"),
		Ticket: row.Str("ticket"), RequireInvite: row.Bool("require_invite"), ResendAt: row.Int64("resend_at"),
	}, nil
}

func FlowSave(f *Flow) {
	set := db.H{
		"email": f.Email, "step": f.Step, "ticket": f.Ticket,
		"require_invite": f.RequireInvite, "resend_at": f.ResendAt, "expires_at": time.Now().Add(FlowTTL).Unix(),
	}
	if app.Db.MustExists("{{auth_flows}}", db.H{"id": f.Id}) {
		app.Db.MustUpdate("{{auth_flows}}", set, db.H{"id": f.Id})
		return
	}
	set["id"] = f.Id
	app.Db.MustInsert("{{auth_flows}}", set)
}

func FlowEnd(id string) {
	app.Db.MustDelete("{{auth_flows}}", db.Or(db.H{"id": id}, db.Lt("expires_at", time.Now().Unix())))
}

// ---------- Шаги ----------

// Email — шаг 1.
func (n *Client) Email(email, ip string) (*Flow, *Err) {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil || len(email) > 200 || !strings.Contains(email, "@") {
		return nil, aerr("email", "Проверьте email.")
	}
	if n == nil {
		return nil, aerr("message", notConfigured)
	}
	f := &Flow{Id: RandToken(24), Email: email}
	res, e := n.Call("check-email", map[string]any{"email": email}, ip)
	if e != nil {
		return nil, e
	}
	if em, _ := res["email"].(string); em != "" {
		f.Email = em
	}
	ex, _ := res["exists"].(bool)
	f.RequireInvite = num(res["require_invite_code"]) == 1
	switch {
	case ex:
		f.Step = "password"
	case num(res["require_email_code"]) == 0 && res["ticket"] != nil:
		f.Step, f.Ticket = "register", fmt.Sprint(res["ticket"])
	default:
		f.Step = "code"
	}
	FlowSave(f)
	if f.Step == "code" {
		if _, e := n.SendCode(f, ip); e != nil {
			return f, e
		}
	}
	return f, nil
}

func num(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		i, _ := strconv.Atoi(x)
		return i
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// Password — шаг 2A. Возвращает id пользователя Amura.
func (n *Client) Password(f *Flow, pass, ip string) (int64, *Err) {
	if pass == "" {
		return 0, aerr("password", "Введите пароль.")
	}
	if n == nil {
		return 0, aerr("message", notConfigured)
	}
	res, e := n.Call("password-login", map[string]any{"email": f.Email, "password": pass}, ip)
	if e != nil {
		return 0, e
	}
	uid, e := syncUser(res)
	if e == nil {
		FlowEnd(f.Id)
	}
	return uid, e
}

// SendCode — отправить код на почту (и для входа без пароля, и для регистрации). Возвращает задержку повтора.
func (n *Client) SendCode(f *Flow, ip string) (int, *Err) {
	if n == nil {
		return 0, aerr("message", notConfigured)
	}
	if wait := f.ResendDelay(); wait > 0 {
		return wait, nil // письмо уже ушло, ждём
	}
	res, e := n.Call("send-code", map[string]any{"email": f.Email}, ip)
	if e != nil {
		return 0, e
	}
	delay := num(res["resend_delay"])
	if delay <= 0 {
		delay = 60
	}
	f.Step = "code"
	f.ResendAt = time.Now().Unix() + int64(delay)
	FlowSave(f)
	return delay, nil
}

// VerifyCode — шаг 2B. Вход (uid>0) или переход к регистрации (uid=0, step=register).
func (n *Client) VerifyCode(f *Flow, code, ip string) (int64, *Err) {
	code = strings.TrimSpace(code)
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return 0, aerr("code", "Код — 6 цифр из письма.")
	}
	if n == nil {
		return 0, aerr("message", notConfigured)
	}
	res, e := n.Call("verify-code", map[string]any{"email": f.Email, "code": code}, ip)
	if e != nil {
		if e.Field == "message" {
			e.Field = "code"
		}
		return 0, e
	}
	if num(res["purpose"]) == 2 {
		f.Step, f.Ticket = "register", fmt.Sprint(res["ticket"])
		FlowSave(f)
		return 0, nil
	}
	uid, e := syncUser(res)
	if e == nil {
		FlowEnd(f.Id)
	}
	return uid, e
}

// Register — шаг 3.
func (n *Client) Register(f *Flow, name, invite, pass, pass2, ip string) (int64, *Err) {
	name = strings.TrimSpace(name)
	if l := len([]rune(name)); l < 2 || l > 40 {
		return 0, aerr("name", "Имя — от 2 до 40 символов.")
	}
	if len(pass) < 8 {
		return 0, aerr("pass", "Пароль — минимум 8 символов.")
	}
	if pass != pass2 {
		return 0, aerr("pass2", "Пароли не совпадают.")
	}
	if f.Step != "register" {
		return 0, aerr("message", "Сначала подтвердите почту.")
	}
	if n == nil {
		return 0, aerr("message", notConfigured)
	}
	if f.RequireInvite && strings.TrimSpace(invite) == "" {
		return 0, aerr("invite", "Нужен код приглашения.")
	}
	res, e := n.Call("register", map[string]any{
		"email": f.Email, "ticket": f.Ticket, "name": name, "utm_source": strings.TrimSpace(invite), "pass": pass, "pass_2": pass2,
	}, ip)
	if e != nil {
		if e.Field == "ticket" {
			e.Msg += " Начните с почты заново."
		}
		return 0, e
	}
	uid, e := syncUser(res)
	if e == nil {
		FlowEnd(f.Id)
	}
	return uid, e
}

// syncUser — профиль Neuronet → запись Amura (по neuronet_uid, потом по email).
func syncUser(p map[string]any) (int64, *Err) {
	nuid := int64(num(p["uid"]))
	email := strings.ToLower(strings.TrimSpace(fmt.Sprint(p["email"])))
	if nuid == 0 || email == "" {
		return 0, aerr("message", "Не удалось завершить вход. Попробуйте ещё раз.")
	}
	name := ""
	if p["first_name"] != nil {
		name = strings.TrimSpace(fmt.Sprint(p["first_name"]))
	}
	if id := app.Db.CreateCommand("SELECT id FROM {{users}} WHERE neuronet_uid = :nuid").BindValue(":nuid", nuid).MustInt(); id > 0 {
		app.Db.CreateCommand("UPDATE {{users}} SET email = :email WHERE id = :id AND email <> :email", db.P{":email": email, ":id": id}).MustExecute()
		return id, nil
	}
	if id := app.Db.CreateCommand("SELECT id FROM {{users}} WHERE email = :email").BindValue(":email", email).MustInt(); id > 0 {
		app.Db.MustUpdate("{{users}}", db.H{"neuronet_uid": nuid}, db.H{"id": id})
		return id, nil
	}
	// локального пароля нет: вход всегда через Neuronet
	id, err := app.Db.Insert("{{users}}", newUserRow(email, name, nuid))
	if err != nil {
		return 0, aerr("message", "Не удалось завершить вход. Попробуйте ещё раз.")
	}
	return id, nil
}

// newUserRow — минимальная запись users: остальное заполнит онбординг. pass_hash = "neuronet": своего пароля нет.
func newUserRow(email, name string, nuid int64) db.H {
	now := time.Now().UTC()
	return db.H{
		"email": email, "pass_hash": "neuronet", "name": name, "neuronet_uid": nuid, "auth_key": RandToken(32),
		"created_at": now, "last_seen": now,
	}
}

// RandToken — случайный id попытки входа / auth_key.
func RandToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
