package controllers

import (
	"math"

	"strings"
	"time"
	"whales.team/go1/db"

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
	Id          int64            `json:"id"`
	Name        string           `json:"name"`
	Age         int              `json:"age"`
	Gender      string           `json:"gender"`
	City        string           `json:"city"`
	DistKm      float64          `json:"distKm"`
	Dist        string           `json:"dist"`
	Verified    bool             `json:"verified"`
	Online      bool             `json:"online"`
	LastSeen    string           `json:"lastSeen"`
	Photos      []string         `json:"photos"`
	Bio         string           `json:"bio"`
	Intent      string           `json:"intent"`
	IntentLabel string           `json:"intentLabel"`
	Interests   []string         `json:"interests"`
	Languages   []users.UserLang `json:"languages"`
	Height      int              `json:"height"`
	Color       int              `json:"color"` // индекс палитры для аватара без фото
}

// toDTO — строка выборки (users.Cols + photo/photos + dist_km) → JSON приложения. Это array_map перед asJson:
// телефон ждёт фиксированные ключи и вычисленные поля (возраст, подписи), которых в базе нет.
func toDTO(r db.Row) UserDTO {
	ls := users.Ts(r.Time("last_seen"))
	if r.Bool("invisible") {
		ls = ""
	}
	ph := []string{}
	for _, f := range users.SplitList(r.Str("photos")) {
		ph = append(ph, photos.Url(f))
	}
	if len(ph) == 0 && r.Str("photo") != "" { // в списках без колонки photos — хотя бы главное
		ph = append(ph, photos.Url(r.Str("photo")))
	}
	return UserDTO{
		Id: r.Int64("id"), Name: r.Str("name"), Age: users.AgeOf(r.Str("birthdate")), Gender: r.Str("gender"), City: r.Str("city"),
		DistKm: math.Round(r.Float("dist_km")*10) / 10, Dist: users.DistLabelKm(r.Float("dist_km")),
		Verified: r.Int("verified") == 2, Online: users.OnlineAt(r["last_seen"]), LastSeen: ls, Photos: ph,
		Bio: r.Str("bio"), Intent: r.Str("intent"), IntentLabel: users.IntentLabels[r.Str("intent")],
		Interests: users.SplitList(r.Str("interests")), Languages: users.ParseLangs(r.Str("languages")), Height: r.Int("height"), Color: users.ColorIndex(r.Int64("id")),
	}
}

func toDTOs(rows db.Rows) []UserDTO { return arr.Map(rows, toDTO) }

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
	d := MeDTO{UserDTO: toDTO(users.RowOf(u, uid)), Email: u.Email, Birthdate: u.Birthdate, Seeking: u.Seeking, Onboarded: u.Onboarded,
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
