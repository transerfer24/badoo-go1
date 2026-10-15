package controllers

import (
	"math"
	"strings"
	"time"

	"badoo/components/photos"
	"badoo/components/users"

	"whales.team/go1/helpers/arr"
	"whales.team/go1/web"
)

/*
API v1 для мобильного приложения (Badoo/mobile) — перенос api_v1.go и authapi.go.

Контракт с приложением не меняется: те же пути /api/v1/*, те же JSON-ответы и те же ошибки
{"error": "<код>", "message": "<текст>"} — приложение их показывает как есть (ApiError в src/lib/api.ts).
Поэтому ошибки здесь не через web.HttpError (у ядра свой формат {"ok": false, …}), а через apiFail.

Авторизация — `Authorization: Bearer <token>` из /api/v1/auth/*: ядро само находит пользователя через
auth.Finder.FindIdentityByAccessToken, CSRF для Bearer не нужен (ApiController).

	ApiController (база)  ← a.auth(onboard, h): 401 unauthorized / 403 onboarding, last_seen, лимит
	ApiAuthController     /api/v1/auth/*       шаги входа через Neuronet, logout
	ApiMeController       /api/v1/me*          анкета, фото, настройки, верификация, push-токены
	ApiPeopleController   /api/v1/encounters, votes, undo, nearby, users/<id>*
	ApiSocialController   /api/v1/likes, visitors, counts, chats*, events
	ApiPremiumController  /api/v1/premium
	ApiMetaController     /api/v1/meta
*/
type ApiController struct {
	web.ApiController
	// NoOnboard — экшены без заполненной анкеты (a.auth(false, …)): профиль, фото, выход, события.
	NoOnboard []string
	// Public — экшены без входа вообще (meta, шаги входа).
	Public []string
}

func (c *ApiController) Behaviors() []web.Behavior {
	rules := []web.AccessRule{}
	if len(c.Public) > 0 { // правило с пустым Actions относится ко всем экшенам — добавляем только при наличии публичных
		rules = append(rules, web.AccessRule{Allow: true, Actions: c.Public})
	}
	rules = append(rules, web.AccessRule{Allow: true, Roles: []string{"@"}})
	return []web.Behavior{
		web.AccessControl{Rules: rules, Deny: func(c *web.Context) error { return apiFail(c, 401, "unauthorized") }},
		apiOnboardFilter{skip: append(append([]string{}, c.NoOnboard...), c.Public...)},
		web.RateLimit{Limit: 300, Window: time.Minute},
	}
}

type apiOnboardFilter struct{ skip []string }

func (f apiOnboardFilter) BeforeAction(c *web.Context, action string) error {
	if c.User.IsGuest {
		return nil // публичный экшен
	}
	users.Touch(c.User.Id)
	for _, a := range f.skip {
		if a == action {
			return nil
		}
	}
	if me := users.Current(c); me == nil || !me.Onboarded {
		return apiFail(c, 403, "onboarding")
	}
	return nil
}

// apiFail — ошибка в формате приложения: {"error": code, "message": msg}.
func apiFail(c *web.Context, status int, code string, msg ...string) error {
	out := web.M{"error": code}
	if len(msg) > 0 {
		out["message"] = msg[0]
	}
	return c.JsonStatus(status, out)
}

// apiBind — JSON-тело в структуру; при ошибке отвечает 400 bad_request и возвращает false.
func apiBind(c *web.Context, v any) bool {
	if err := c.Bind(v); err != nil {
		_ = apiFail(c, 400, "bad_request", "Некорректный запрос")
		return false
	}
	return true
}

// ---------- DTO (api_v1.go) ----------

type PhotoDTO struct {
	Id  int64  `json:"id"`
	Url string `json:"url"`
}

type UserDTO struct {
	Id          int64    `json:"id"`
	Name        string   `json:"name"`
	Age         int      `json:"age"`
	Gender      string   `json:"gender"`
	City        string   `json:"city"`
	DistKm      float64  `json:"distKm"`
	Dist        string   `json:"dist"`
	Verified    bool     `json:"verified"`
	Online      bool     `json:"online"`
	LastSeen    string   `json:"lastSeen"`
	Photos      []string `json:"photos"`
	Bio         string   `json:"bio"`
	Intent      string   `json:"intent"`
	IntentLabel string   `json:"intentLabel"`
	Interests   []string `json:"interests"`
	Height      int      `json:"height"`
	Color       int      `json:"color"` // индекс палитры для аватара без фото
}

func toDTO(u *users.User) UserDTO {
	ph := u.Photos
	if ph == nil {
		ph = []string{}
	}
	ints := u.InterestList()
	if ints == nil {
		ints = []string{}
	}
	ls := users.Ts(u.LastSeen)
	if u.Invisible {
		ls = ""
	}
	return UserDTO{u.Id, u.Name, u.Age(), u.Gender, u.City, math.Round(u.DistKm*10) / 10, u.DistLabel(), u.IsVerified(), u.Online(),
		ls, ph, u.Bio, u.Intent, u.IntentLabel(), ints, u.Height, users.ColorIndex(u.Id)}
}

func toDTOs(list []*users.User) []UserDTO { return arr.Map(list, toDTO) }

type MeDTO struct {
	UserDTO
	Email        string     `json:"email"`
	Birthdate    string     `json:"birthdate"`
	Seeking      string     `json:"seeking"`
	Onboarded    bool       `json:"onboarded"`
	Verification int        `json:"verification"` // 0 нет, 1 на проверке, 2 подтверждён
	Premium      bool       `json:"premium"`
	PremiumUntil string     `json:"premiumUntil"`
	Invisible    bool       `json:"invisible"`
	AgeMin       int        `json:"ageMin"`
	AgeMax       int        `json:"ageMax"`
	MaxKm        int        `json:"maxKm"`
	VerifiedOnly bool       `json:"verifiedOnly"`
	PhotoList    []PhotoDTO `json:"photoList"`
}

// meDTO — моя анкета для ответов /me, входа, фото, настроек (writeMe в оригинале).
func meDTO(uid int64) (MeDTO, bool) {
	u := users.ById(uid)
	if u == nil {
		return MeDTO{}, false
	}
	d := MeDTO{UserDTO: toDTO(u), Email: u.Email, Birthdate: u.Birthdate, Seeking: u.Seeking, Onboarded: u.Onboarded,
		Verification: u.Verified, Premium: u.IsPremium(), PremiumUntil: users.Ts(u.PremiumUntil), Invisible: u.Invisible,
		AgeMin: u.AgeMin, AgeMax: u.AgeMax, MaxKm: u.MaxKm, VerifiedOnly: u.VerifiedOnly, PhotoList: []PhotoDTO{}}
	d.LastSeen = users.Ts(u.LastSeen) // себе last_seen виден и в режиме невидимки
	for _, p := range photos.Mine(uid) {
		d.PhotoList = append(d.PhotoList, PhotoDTO{p.Id, p.Url})
	}
	return d, true
}

func writeMe(c *web.Context, uid int64) error {
	d, ok := meDTO(uid)
	if !ok {
		return apiFail(c, 500, "db")
	}
	return c.Json(d)
}

// bearer — токен текущего запроса (для logout).
func bearer(c *web.Context) string {
	h := c.Request.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
