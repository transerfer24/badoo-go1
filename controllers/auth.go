package controllers

import (
	"time"

	"badoo/components/auth"
	"badoo/components/neuronet"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/web"
)

/*
AuthController — вход, выход, онбординг (веб-версия). Шаги входа — components/neuronet, здесь транспорт:
форма → шаг → редирект (перенос handlers_auth.go + веб-части authapi.go).

Попытка входа (flow) между шагами лежит в сессии Go1, а не в отдельной cookie amura_flow — у сессии уже
есть подпись и срок.

	GET  /login            форма текущего шага
	POST /login/<step>     email | password | send-code | code | register | restart
	POST /logout
	GET/POST /onboarding   анкета после регистрации
*/
type AuthController struct{ web.Controller }

const flowKey = "auth.flow"

func (c *AuthController) Behaviors() []web.Behavior {
	return []web.Behavior{
		web.AccessControl{Rules: []web.AccessRule{
			{Allow: true, Actions: []string{"onboarding", "logout"}, Roles: []string{"@"}},
			{Allow: true, Actions: []string{"login", "step"}},
		}},
		// лимит попыток по IP: 10 за 10 минут на email/пароль (как authLimit в старом бэкенде)
		web.RateLimit{Limit: 10, Window: 10 * time.Minute, Only: []string{"step"}},
	}
}

func (c *AuthController) nn() *neuronet.Client { return neuronet.New() }

// flow — текущая попытка входа из сессии (nil — начать с почты).
func (c *AuthController) flow() *neuronet.Flow {
	id := c.Session.GetString(flowKey)
	if id == "" {
		return nil
	}
	f, e := neuronet.FlowGet(id)
	if e != nil {
		c.Session.Remove(flowKey)
		return nil
	}
	return f
}

func (c *AuthController) ActionLogin() error {
	if !c.User.IsGuest {
		return c.Redirect("/encounters")
	}
	return c.signin(c.flow(), nil, nil)
}

// signin — экран входа на текущем шаге; errs — ошибки по полям, form — введённое.
func (c *AuthController) signin(f *neuronet.Flow, errs, form map[string]string) error {
	if errs == nil {
		errs = map[string]string{}
	}
	if form == nil {
		form = map[string]string{}
	}
	res := web.M{
		"step": "email", "email": "", "require_invite": false, "neuronet": c.nn().Enabled(), "resend": 0,
		"errors": errs, "form": form,
	}
	if f != nil {
		res["step"], res["email"], res["require_invite"] = f.Step, f.Email, f.RequireInvite
		res["resend"] = f.ResendDelay()
	}
	return c.Render("signin", res)
}

func (c *AuthController) ActionStep() error {
	step := c.Params["step"]
	ip := c.Request.UserIP
	nn := c.nn()
	form := map[string]string{"email": c.Post("email"), "name": c.Post("name"), "invite": c.Post("invite")}
	fail := func(f *neuronet.Flow, e *neuronet.Err) error {
		if e.Field == "flow" {
			c.Session.Remove(flowKey)
			f = nil
		}
		return c.signin(f, map[string]string{e.Field: e.Msg}, form)
	}

	if step == "restart" {
		if f := c.flow(); f != nil {
			neuronet.FlowEnd(f.Id)
		}
		c.Session.Remove(flowKey)
		return c.Redirect("/login")
	}
	if step == "email" {
		f, e := nn.Email(c.Post("email"), ip)
		if f != nil {
			c.Session.Set(flowKey, f.Id)
		}
		if e != nil {
			return fail(f, e)
		}
		return c.Redirect("/login")
	}

	f := c.flow()
	if f == nil {
		return fail(nil, &neuronet.Err{Field: "email", Msg: "Попытка входа устарела — начните с почты заново."})
	}
	var uid int64
	var e *neuronet.Err
	switch step {
	case "password":
		uid, e = nn.Password(f, c.Post("password"), ip)
	case "send-code":
		if _, e = nn.SendCode(f, ip); e == nil {
			return c.Redirect("/login")
		}
	case "code":
		uid, e = nn.VerifyCode(f, c.Post("code"), ip)
	case "register":
		uid, e = nn.Register(f, c.Post("name"), c.Post("invite"), c.Post("pass"), c.Post("pass2"), ip)
	default:
		return c.NotFound()
	}
	if e != nil {
		return fail(f, e)
	}
	if uid == 0 { // код подтвердил почту — дальше регистрация
		return c.Redirect("/login")
	}
	c.Session.Remove(flowKey)
	return c.login(uid)
}

// login — вход по id: identity в сессию Go1 + remember-me на 30 дней (было: таблица sessions + cookie amura_session).
func (c *AuthController) login(uid int64) error {
	id, err := auth.Finder{}.FindIdentity(uid)
	if err != nil || id == nil {
		return c.signin(nil, map[string]string{"message": "Не удалось завершить вход. Попробуйте ещё раз."}, nil)
	}
	if err := c.User.Login(id, 30*24*time.Hour); err != nil {
		return err
	}
	me := users.Current(c.Context)
	if me != nil && !me.Onboarded {
		return c.Redirect("/onboarding")
	}
	return c.Redirect("/encounters")
}

func (c *AuthController) ActionLogout() error {
	_ = c.User.Logout()
	return c.Redirect("/")
}

func (c *AuthController) ActionOnboarding() error {
	me := users.Current(c.Context)
	if me != nil && me.Onboarded {
		return c.Redirect("/encounters")
	}
	res := web.M{"cities": users.Cities, "max": users.MaxBirthdate(), "error": ""}
	if !c.Request.IsPost {
		return c.Render("onboarding", res)
	}

	set, errMsg := users.ParseProfileForm(c.Context, true)
	if errMsg != "" {
		res["error"] = errMsg
		return c.Render("onboarding", res)
	}
	set["onboarded"] = true
	if _, err := app.Db.Update("{{users}}", set, map[string]any{"id": c.User.Id}); err != nil {
		res["error"] = "Не удалось сохранить."
		return c.Render("onboarding", res)
	}
	c.Session.SetFlash("ok", "Анкета создана. Добавьте фото — без них вас почти не видят.")
	return c.Redirect("/me")
}
