package control

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
SessionsController — входы и сессии.

  - Сессии приложения — Bearer-токены мобильного приложения (sessions): кто, платформа, когда вошёл, до когда
    действует. «Завершить» удаляет один токен: на этом устройстве приложение получит 401 и покажет вход — так же,
    как после «выйти везде» в карточке (приложение это уже умеет, контракт API не меняется).
  - Попытки входа — незавершённые шаги входа (auth_flows): почта → пароль/код → регистрация. Завершённые и
    просроченные попытки удаляются при следующем входе, поэтому здесь — те, кто сейчас входит или бросил на шаге.

Токен целиком в админку не уходит: строку определяет key — начало sha256 от токена.
*/
type SessionsController struct{ web.Controller }

func (c *SessionsController) Behaviors() []web.Behavior { return access("sessions", "index") }

func (c *SessionsController) Init() { c.Layout = "control" }

func (c *SessionsController) ActionIndex() error {
	return c.Render("sessions", web.M{"section": "sessions"})
}

// sessionKey — идентификатор сессии для админки вместо токена
func sessionKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:8])
}

func (c *SessionsController) ActionGetStats() error {
	now := time.Now().UTC()
	p := db.P{":now": now, ":day": now.Add(-24 * time.Hour), ":week": now.Add(-7 * 24 * time.Hour)}
	stats := app.Db.CreateCommand(`
		SELECT (SELECT COUNT(*) FROM {{sessions}} WHERE expires_at > :now) AS active,
		       (SELECT COUNT(*) FROM {{sessions}} WHERE expires_at <= :now) AS expired,
		       (SELECT COUNT(DISTINCT user_id) FROM {{sessions}} WHERE expires_at > :now) AS users,
		       (SELECT COUNT(*) FROM {{sessions}} WHERE created_at >= :day) AS new_day,
		       (SELECT COUNT(*) FROM {{sessions}} WHERE created_at >= :week) AS new_week,
		       (SELECT COUNT(*) FROM {{push_tokens}}) AS push,
		       (SELECT COUNT(DISTINCT user_id) FROM {{push_tokens}}) AS push_users,
		       (SELECT COUNT(*) FROM {{auth_flows}} WHERE expires_at > :unix) AS flows
	`, db.P{":now": p[":now"], ":day": p[":day"], ":week": p[":week"], ":unix": now.Unix()}).MustRow()
	platforms := app.Db.CreateCommand(`
		SELECT s.platform, COUNT(*) AS sessions, COUNT(DISTINCT s.user_id) AS users,
		       (SELECT COUNT(*) FROM {{push_tokens}} t WHERE t.platform = s.platform) AS push
		FROM {{sessions}} s WHERE s.expires_at > :now GROUP BY s.platform ORDER BY sessions DESC`, db.P{":now": now}).MustAll()
	return c.Json(web.M{"status": 200, "stats": stats, "platforms": platforms})
}

func (c *SessionsController) ActionGetSessions() error {
	now := time.Now().UTC()
	q := app.Db.Query().
		Select("s.token", "s.user_id", "s.platform", "s.created_at", "s.expires_at",
			"u.name", "u.email", "u.banned", "u.last_seen",
			"(SELECT COUNT(*) FROM {{sessions}} x WHERE x.user_id = s.user_id AND x.expires_at > :now) AS user_sessions",
			"(SELECT ph.file FROM {{photos}} ph WHERE ph.user_id = s.user_id ORDER BY ph.pos, ph.id LIMIT 1) AS photo").
		From("{{sessions}} s").
		LeftJoin("{{users}} u", db.Raw("u.id = s.user_id")).
		Params(db.P{":now": now})
	if s := strings.TrimSpace(c.Post("search")); s != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
		q.AndWhere(db.Raw("(u.name LIKE :s OR u.email LIKE :s OR s.user_id = :sid)", db.P{":s": "%" + s + "%", ":sid": id}))
	}
	if pl := strings.TrimSpace(c.Post("platform")); pl != "" {
		q.AndWhere(db.H{"s.platform": pl})
	}
	switch c.Post("state") {
	case "active":
		q.AndWhere(db.Raw("s.expires_at > :now2", db.P{":now2": now}))
	case "expired":
		q.AndWhere(db.Raw("s.expires_at <= :now2", db.P{":now2": now}))
	}
	if t := since(c.Post("period")); !t.IsZero() {
		q.AndWhere(db.Raw("s.created_at >= :since", db.P{":since": t}))
	}
	q.OrderBy("s.created_at DESC")
	p := mustPaginate(q, max(1, c.PostInt("page", 1)), 50)
	for _, r := range p.Items {
		t := r.Str("token")
		r["key"] = sessionKey(t)
		r["token"] = t[:min(6, len(t))]
	}
	return c.Json(web.M{"status": 200, "items": prepare(p.Items), "count": p.Total})
}

// Завершить одну сессию приложения: uid + key (начало sha256 токена)
func (c *SessionsController) ActionEnd() error {
	uid, key := c.PostInt64("uid"), c.Post("key")
	for _, r := range app.Db.CreateCommand("SELECT token, platform, created_at FROM {{sessions}} WHERE user_id = :uid").BindValue(":uid", uid).MustAll() {
		if key == "" || sessionKey(r.Str("token")) != key {
			continue
		}
		app.Db.MustDelete("{{sessions}}", db.H{"token": r.Str("token"), "user_id": uid})
		logAction(c.Context, uid, "session-end", strings.TrimSpace(r.Str("platform")+" · вход "+r.Time("created_at").Format("02.01.2006 15:04")))
		return c.Json(web.M{"status": 200})
	}
	return c.Json(web.M{"status": 404, "errors": web.M{"modal_message": "Сессия не найдена — возможно, уже завершена"}})
}

// Незавершённые попытки входа. expires_at/resend_at здесь — unix-секунды (BIGINT), поэтому под другими именами:
// prepare() переводит в unix только колонки дат.
func (c *SessionsController) ActionGetFlows() error {
	now := time.Now().Unix()
	q := app.Db.Query().
		Select("f.email", "f.step", "f.local", "f.require_invite", "f.resend_at AS resend_ts", "f.expires_at AS expires_ts",
			"u.id AS user_id", "u.name", "u.banned").
		From("{{auth_flows}} f").
		LeftJoin("{{users}} u", db.Raw("u.email = f.email"))
	if c.Post("expired") != "1" {
		q.AndWhere(db.Raw("f.expires_at > :now", db.P{":now": now}))
	}
	if s := strings.TrimSpace(c.Post("search")); s != "" {
		q.AndWhere(db.Raw("f.email LIKE :s", db.P{":s": "%" + s + "%"}))
	}
	q.OrderBy("f.expires_at DESC")
	p := mustPaginate(q, max(1, c.PostInt("page", 1)), 50)
	// сколько незавершённых попыток на ту же почту — много подряд = человек не может войти (или перебор)
	for _, r := range p.Items {
		r["same_email"] = app.Db.CreateCommand("SELECT COUNT(*) FROM {{auth_flows}} WHERE email = :e").BindValue(":e", r.Str("email")).MustInt()
	}
	return c.Json(web.M{"status": 200, "items": prepare(p.Items), "count": p.Total})
}
