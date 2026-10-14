package controllers

import (
	"time"

	"badoo/components/users"

	"whales.team/go1/web"
)

/*
AppController — база для страниц кабинета: то, что в старом бэкенде делала обёртка a.auth(onboard, h).

  - гость → /login (AccessControl, роль "@");
  - анкета не заполнена → /onboarding (кроме самого онбординга и выхода);
  - last_seen не чаще раза в минуту.

Контроллеры страниц встраивают его: type MeController struct{ AppController }. Экшены, которым анкета не нужна
(фото при онбординге, удаление аккаунта — auth(false) в оригинале), перечисляются в NoOnboard.
*/
type AppController struct {
	web.Controller
	// NoOnboard — экшены, доступные без заполненной анкеты (как a.auth(false, …)).
	NoOnboard []string
}

func (c *AppController) Behaviors() []web.Behavior {
	return []web.Behavior{
		web.AccessControl{
			Rules: []web.AccessRule{{Allow: true, Roles: []string{"@"}}},
			Deny: func(c *web.Context) error {
				if c.Request.IsAjax || c.Request.IsJSON {
					return web.ErrUnauthorized("unauthorized")
				}
				return c.Redirect("/login")
			},
		},
		onboardFilter{skip: c.NoOnboard},
		web.RateLimit{Limit: 300, Window: time.Minute},
	}
}

type onboardFilter struct{ skip []string }

func (f onboardFilter) BeforeAction(c *web.Context, action string) error {
	for _, a := range f.skip {
		if a == action {
			users.Touch(c.User.Id)
			return nil
		}
	}
	me := users.Current(c)
	if me == nil {
		return c.Redirect("/login")
	}
	if !me.Onboarded {
		if c.Request.IsAjax || c.Request.IsJSON {
			return web.ErrForbidden("onboarding")
		}
		return c.Redirect("/onboarding")
	}
	users.Touch(c.User.Id)
	return nil
}
