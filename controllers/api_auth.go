package controllers

import (
	"time"

	"badoo/components/auth"
	"badoo/components/neuronet"

	"whales.team/go1/web"
)

/*
ApiAuthController — пошаговый вход приложения через Neuronet (перенос authapi.go):

	POST /api/v1/auth/email        {email}                     → flowDTO (step: password | code)
	POST /api/v1/auth/password     {flow, password}            → {token, me}
	POST /api/v1/auth/send-code    {flow}                      → {resendDelay, step: "code"}
	POST /api/v1/auth/verify-code  {flow, code}                → {token, me} | flowDTO (step: register)
	POST /api/v1/auth/register     {flow, name, invite, pass, pass2} → {token, me}
	POST /api/v1/auth/logout       Bearer                      → {ok: true}
	POST /api/v1/auth/signup|login — старый вход по паролю Amura: 410, приложение просит обновиться.

Логика шагов — components/neuronet (та же, что у веб-входа); здесь только транспорт.
*/
type ApiAuthController struct{ ApiController }

func (c *ApiAuthController) Init() {
	c.Public = []string{"email", "password", "send-code", "verify-code", "register", "signup", "login"}
	c.NoOnboard = []string{"logout"}
}

func (c *ApiAuthController) Behaviors() []web.Behavior {
	// первый шаг публично отвечает, есть ли аккаунт; пароль — перебор: лимит по IP, как rateOK в оригинале
	return append(c.ApiController.Behaviors(),
		web.RateLimit{Limit: 10, Window: 10 * time.Minute, Only: []string{"email", "password", "register"},
			Key: func(c *web.Context) string { return "ip:" + c.Request.UserIP }})
}

type flowDTO struct {
	Flow          string `json:"flow"`
	Step          string `json:"step"`
	Email         string `json:"email"`
	RequireInvite bool   `json:"requireInvite"`
	ResendDelay   int    `json:"resendDelay,omitempty"`
	Neuronet      bool   `json:"neuronet"`
}

func flowOut(f *neuronet.Flow) flowDTO {
	return flowDTO{Flow: f.Id, Step: f.Step, Email: f.Email, RequireInvite: f.RequireInvite, ResendDelay: f.ResendDelay(), Neuronet: true}
}

func authFail(c *web.Context, e *neuronet.Err) error {
	code := 400
	if e.Field == "flow" {
		code = 410
	}
	return apiFail(c, code, e.Field, e.Msg)
}

type stepIn struct {
	Email    string `json:"email"`
	Flow     string `json:"flow"`
	Password string `json:"password"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Invite   string `json:"invite"`
	Pass     string `json:"pass"`
	Pass2    string `json:"pass2"`
	Platform string `json:"platform"`
}

func (c *ApiAuthController) ActionEmail() error {
	var in stepIn
	if !apiBind(c.Context, &in) {
		return nil
	}
	f, e := neuronet.New().Email(in.Email, c.Request.UserIP)
	if e != nil && f == nil {
		return authFail(c.Context, e)
	}
	out := flowOut(f)
	if e != nil { // попытка заведена, но письмо не ушло — экран кода покажет предупреждение
		return c.Json(web.M{"flow": out.Flow, "step": out.Step, "email": out.Email, "requireInvite": out.RequireInvite, "neuronet": true, "warning": e.Msg})
	}
	return c.Json(out)
}

func (c *ApiAuthController) ActionPassword() error   { return c.step("password") }
func (c *ApiAuthController) ActionSendCode() error   { return c.step("send-code") }
func (c *ApiAuthController) ActionVerifyCode() error { return c.step("verify-code") }
func (c *ApiAuthController) ActionRegister() error   { return c.step("register") }

func (c *ApiAuthController) step(kind string) error {
	var in stepIn
	if !apiBind(c.Context, &in) {
		return nil
	}
	f, e := neuronet.FlowGet(in.Flow)
	if e != nil {
		return authFail(c.Context, e)
	}
	nn, ip := neuronet.New(), c.Request.UserIP
	var uid int64
	switch kind {
	case "password":
		uid, e = nn.Password(f, in.Password, ip)
	case "send-code":
		var delay int
		if delay, e = nn.SendCode(f, ip); e == nil {
			return c.Json(web.M{"resendDelay": delay, "step": "code"})
		}
	case "verify-code":
		uid, e = nn.VerifyCode(f, in.Code, ip)
	case "register":
		uid, e = nn.Register(f, in.Name, in.Invite, in.Pass, in.Pass2, ip)
	}
	if e != nil {
		return authFail(c.Context, e)
	}
	if uid == 0 { // код подтвердил почту — дальше регистрация
		return c.Json(flowOut(f))
	}
	return issueToken(c.Context, uid, in.Platform)
}

// issueToken — вход состоялся: Bearer-токен и анкета.
func issueToken(c *web.Context, uid int64, platform string) error {
	tok, err := auth.NewToken(uid, platform)
	if err != nil {
		return apiFail(c, 500, "db")
	}
	me, _ := meDTO(uid)
	return c.Json(web.M{"token": tok, "me": me})
}

func (c *ApiAuthController) ActionLogout() error {
	if t := bearer(c.Context); t != "" {
		auth.DropToken(t, c.User.Id)
	}
	return c.Json(web.M{"ok": true})
}

// Старый вход по паролю Amura (auth/signup, auth/login) убран: регистрация только через Neuronet.
func (c *ApiAuthController) ActionSignup() error { return c.gone() }
func (c *ApiAuthController) ActionLogin() error  { return c.gone() }
func (c *ApiAuthController) gone() error {
	return apiFail(c.Context, 410, "message", "Вход теперь по шагам через почту — обновите приложение.")
}
