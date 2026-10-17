package controllers

import (
	"crypto/subtle"
	"log/slog"
	"strings"
	"time"

	"badoo/components/auth"

	"whales.team/go1/app"
	"whales.team/go1/config"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
QuickauthController — быстрый вход под своим пользователем по ссылке, чтобы проверять страницы без кода из
Neuronet (как QuickauthController в WealthBook/payment.surfer.team):

	GET /quickauth/login?key=<QUICKAUTH_KEY>&email=<почта>[&to=/nearby]

Работает только если в .env задан QUICKAUTH_KEY (от 32 символов) и почта есть в QUICKAUTH_EMAILS
(через запятую — список своих аккаунтов). Любой отказ — обычная 404, причина только в логе.
Сессия короткая (2 часа), без remember-me. Лимит 10 попыток в минуту по IP — ключ не перебрать.
*/
type QuickauthController struct{ web.Controller }

func (c *QuickauthController) Behaviors() []web.Behavior {
	return []web.Behavior{web.RateLimit{Limit: 10, Window: time.Minute}}
}

func (c *QuickauthController) ActionLogin() error {
	key := config.Env("QUICKAUTH_KEY")
	email := strings.ToLower(strings.TrimSpace(c.Get("email")))
	deny := func(why string) error {
		slog.Warn("quickauth: отказ", "why", why, "email", email, "ip", c.Request.UserIP)
		return c.NotFound()
	}
	if len(key) < 32 || subtle.ConstantTimeCompare([]byte(c.Get("key")), []byte(key)) != 1 {
		return deny("ключ")
	}
	allowed := false
	for _, e := range strings.Split(config.Env("QUICKAUTH_EMAILS"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" && e == email {
			allowed = true
		}
	}
	if !allowed {
		return deny("почта не в QUICKAUTH_EMAILS")
	}
	uid := int64(app.Db.CreateCommand("SELECT id FROM {{users}} WHERE LOWER(email) = :email", db.P{":email": email}).MustInt())
	if uid == 0 {
		return deny("нет такого пользователя")
	}
	id, err := auth.Finder{}.FindIdentity(uid)
	if err != nil || id == nil {
		return deny("identity")
	}
	if err := c.User.Login(id, 2*time.Hour); err != nil {
		return err
	}
	slog.Info("quickauth: вход", "uid", uid, "email", email, "ip", c.Request.UserIP)
	to := c.Get("to")
	if !web.IsLocalUrl(to) {
		to = "/encounters"
	}
	return c.Redirect(to)
}
