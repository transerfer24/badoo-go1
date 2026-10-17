// Package users — анкета пользователя (перенос backend/models.go + справочники из handlers_auth.go).
//
// User — обычная структура под строку таблицы users, а не модель с поведением: заполняется через
// Row.Scan (имена колонок = snake_case полей), методы — только то, что нужно шаблонам
// (возраст, аватар, Premium). SQL остаётся в экшенах.
package users

import (
	"badoo/components/photos"

	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/helpers/arr"
	"whales.team/go1/web"
)

// User — текущий пользователь ($user_info в WealthBook): identity для экшенов и лейаута (me.Id, me.Lat, me.IsPremium()).
// Чужие анкеты в списках и на экранах — не структуры, а строки выборок (sql.go: users.Cols + photo + dist_km),
// как queryAll() в WealthBook; поля для них считают функции шаблонов users.Funcs.
type User struct {
	Id           int64     `db:"id"`
	Email        string    `db:"email"`
	Name         string    `db:"name"`
	Birthdate    string    `db:"birthdate"` // YYYY-MM-DD
	Gender       string    `db:"gender"`
	Seeking      string    `db:"seeking"`
	Bio          string    `db:"bio"`
	City         string    `db:"city"`
	Lat          float64   `db:"lat"`
	Lng          float64   `db:"lng"`
	Height       int       `db:"height"`
	Intent       string    `db:"intent"`
	Interests    string    `db:"interests"`
	Verified     int       `db:"verified"`
	PremiumUntil time.Time `db:"premium_until"`
	Invisible    bool      `db:"invisible"`
	AgeMin       int       `db:"age_min"`
	AgeMax       int       `db:"age_max"`
	MaxKm        int       `db:"max_km"`
	VerifiedOnly bool      `db:"verified_only"`
	Onboarded    bool      `db:"onboarded"`
	LastSeen     time.Time `db:"last_seen"`

	Photos []string `db:"-"` // URL-ы (главное — первым)
}

// ById — анкета с фото. nil — нет такой.
func ById(id int64) *User {
	u := &User{}
	row := app.Db.CreateCommand("SELECT * FROM {{users}} WHERE id = :id").BindValue(":id", id).MustRow()
	if row == nil {
		return nil
	}
	if err := row.Scan(u); err != nil {
		panic(db.MustError{Err: err})
	}
	u.Photos = PhotoUrls(id)
	return u
}

// PhotoUrls — адреса фото анкеты по порядку.
func PhotoUrls(uid int64) []string {
	files := app.Db.CreateCommand("SELECT file FROM {{photos}} WHERE user_id = :uid ORDER BY pos, id").BindValue(":uid", uid).MustStrings()
	return arr.Map(files, photos.Url)
}

/*
Current — анкета текущего пользователя для лейаута: {{$me := me .Ctx}}. Считается один раз на запрос
(c.Values), nil для гостя. Identity (c.User.Identity) хранит только id/email — полная анкета нужна
навигации (аватар, Premium, onboarded), поэтому здесь, а не в каждом экшене.
*/
func Current(c *web.Context) *User {
	if v, ok := c.GetValue("users.me").(*User); ok {
		return v
	}
	if c.User.IsGuest {
		return nil
	}
	u := ById(c.User.Id)
	c.SetValue("users.me", u)
	return u
}

// Главное фото или сгенерированный аватар.
func (u *User) Avatar() string {
	if len(u.Photos) > 0 {
		return u.Photos[0]
	}
	return fmt.Sprintf("/avatar/%d.svg", u.Id)
}

func AgeAt(b, now time.Time) int {
	age := now.Year() - b.Year()
	if now.Month() < b.Month() || (now.Month() == b.Month() && now.Day() < b.Day()) {
		age--
	}
	return age
}

func (u *User) IsPremium() bool { return !u.PremiumUntil.IsZero() && u.PremiumUntil.After(time.Now()) }

// Filled — заполненность анкеты в процентах (блок «Ваш профиль» на сайте). Считается только по тому, что
// анкета реально хранит: фото, о себе, интересы, рост, цель, проверка селфи.
func (u *User) Filled() int {
	n := 0
	switch {
	case len(u.Photos) >= 3:
		n += 35
	case len(u.Photos) > 0:
		n += 20
	}
	if strings.TrimSpace(u.Bio) != "" {
		n += 15
	}
	if len(SplitList(u.Interests)) >= 3 {
		n += 15
	}
	if u.Height > 0 {
		n += 5
	}
	if u.Intent != "" {
		n += 10
	}
	if u.IsVerified() {
		n += 20
	}
	return n
}

// FillHint — что добавить первым, чтобы анкета стала полнее ("" — всё заполнено).
func (u *User) FillHint() string {
	switch {
	case len(u.Photos) < 3:
		return "Добавьте ещё фотографий — с тремя и больше анкету открывают заметно чаще."
	case !u.IsVerified():
		return "Пройдите проверку селфи — рядом с именем появится галочка."
	case strings.TrimSpace(u.Bio) == "":
		return "Напишите пару фраз о себе — с ними проще начать разговор."
	case len(SplitList(u.Interests)) < 3:
		return "Отметьте хотя бы три интереса — по ним находят общее."
	}
	return ""
}

func (u *User) IsVerified() bool { return u.Verified == 2 }

func (u *User) PremiumDate() string {
	if u.PremiumUntil.IsZero() {
		return ""
	}
	return u.PremiumUntil.Local().Format("02.01.2006")
}

func (u *User) InterestList() []string {
	out := []string{}
	for _, s := range strings.Split(u.Interests, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var IntentLabels = map[string]string{
	"serious": "Серьёзные отношения",
	"casual":  "Свидания без обязательств",
	"friends": "Новые друзья",
	"chat":    "Просто общение",
	"unsure":  "Пока не решил(а)",
}

var GenderLabels = map[string]string{"m": "Мужчина", "f": "Женщина", "x": "Небинарная персона"}

func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// TsLayout — формат дат в JSON для приложения (как в старом бэкенде).
const TsLayout = "2006-01-02T15:04:05Z"

func Ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TsLayout)
}

// Touch — last_seen не чаще раза в минуту (было: sync.Map в auth.go; здесь — кеш приложения, он один на процесс).
func Touch(uid int64) {
	ok, _ := app.Cache.Add(fmt.Sprintf("users.touch.%d", uid), 1, time.Minute)
	if !ok {
		return
	}
	app.Db.MustUpdate("{{users}}", db.H{"last_seen": time.Now().UTC()}, db.H{"id": uid})
}

// ---------- Справочники анкеты ----------

type City struct {
	Name     string
	Lat, Lng float64
}

var Cities = []City{
	{"Мадрид", 40.4168, -3.7038}, {"Барселона", 41.3874, 2.1686}, {"Валенсия", 39.4699, -0.3763},
	{"Севилья", 37.3891, -5.9845}, {"Малага", 36.7213, -4.4214}, {"Аликанте", 38.3452, -0.4810},
	{"Бильбао", 43.2630, -2.9350}, {"Сарагоса", 41.6488, -0.8891}, {"Пальма", 39.5696, 2.6502},
	{"Гранада", 37.1773, -3.5986}, {"Марбелья", 36.5101, -4.8825}, {"Торревьеха", 37.9787, -0.6822},
}

func CityByName(n string) (City, bool) {
	for _, c := range Cities {
		if c.Name == n {
			return c, true
		}
	}
	return City{}, false
}

var InterestOptions = []string{
	"Путешествия", "Музыка", "Кино", "Спорт", "Йога", "Бег", "Кулинария", "Вино", "Кофе",
	"Книги", "Фотография", "Искусство", "Танцы", "Море", "Горы", "Собаки", "Кошки",
	"Игры", "Технологии", "Стартапы", "Языки", "Волонтёрство", "Сёрфинг", "Велосипед",
}

func ValidInterest(s string) bool {
	for _, x := range InterestOptions {
		if x == s {
			return true
		}
	}
	return false
}

func MaxBirthdate() string { return time.Now().AddDate(-18, 0, 0).Format("2006-01-02") }

/*
ParseProfileForm — общий разбор полей анкеты (онбординг и редактирование), как parseProfileForm в handlers_auth.go.
Возвращает поля для UPDATE и текст ошибки ("" — всё хорошо). full=true — онбординг (без роста и интересов).
*/
func ParseProfileForm(c *web.Context, full bool) (db.H, string) {
	name := strings.TrimSpace(c.Post("name"))
	birthdate := c.Post("birthdate")
	gender, seeking, city := c.Post("gender"), c.Post("seeking"), c.Post("city")
	bio, intent := strings.TrimSpace(c.Post("bio")), c.Post("intent")

	if l := len([]rune(name)); l < 2 || l > 40 {
		return nil, "Имя — от 2 до 40 символов."
	}
	b, err := time.Parse("2006-01-02", birthdate)
	if err != nil {
		return nil, "Укажите дату рождения."
	}
	if age := AgeAt(b, time.Now()); age < 18 || age > 100 {
		return nil, "Amura только для тех, кому исполнилось 18."
	}
	if GenderLabels[gender] == "" {
		return nil, "Выберите пол."
	}
	if seeking != "m" && seeking != "f" && seeking != "any" {
		return nil, "Выберите, кого вы ищете."
	}
	ct, ok := CityByName(city)
	if !ok {
		return nil, "Выберите город."
	}
	lat, lng := ct.Lat, ct.Lng
	// Если браузер отдал точные координаты — используем их (в пределах 60 км от города).
	plat, err1 := strconv.ParseFloat(c.Post("lat"), 64)
	plng, err2 := strconv.ParseFloat(c.Post("lng"), 64)
	if err1 == nil && err2 == nil && HaversineKm(plat, plng, ct.Lat, ct.Lng) < 60 {
		lat, lng = plat, plng
	}
	if len([]rune(bio)) > 500 {
		return nil, "О себе — не больше 500 символов."
	}
	if intent != "" && IntentLabels[intent] == "" {
		intent = ""
	}
	set := db.H{"name": name, "birthdate": birthdate, "gender": gender, "seeking": seeking, "city": city, "lat": lat, "lng": lng, "bio": bio, "intent": intent}
	if !full {
		if h := c.PostInt("height"); h >= 120 && h <= 230 {
			set["height"] = h
		}
		list := []string{}
		for _, it := range c.PostSlice("interests") {
			if ValidInterest(it) && len(list) < 10 {
				list = append(list, it)
			}
		}
		set["interests"] = strings.Join(list, ",")
	}
	return set, ""
}

// Counts — счётчики навигации: новые симпатии без ответа и непрочитанные (перенос counts() из render.go).
// {{$counts := counts .Ctx}} в лейауте, один раз на запрос; у гостя — нули.
// UnreadFn — счётчик непрочитанных; ставит пакет chat (chat.Unread), сам users о сообщениях не знает (иначе цикл импортов).
var UnreadFn = func(uid int64) int { return 0 }

type CountsT struct {
	Likes  int
	Unread int
	Guests int
}

func Counts(c *web.Context) CountsT {
	if v, ok := c.GetValue("users.counts").(CountsT); ok {
		return v
	}
	var n CountsT
	if !c.User.IsGuest {
		uid := c.User.Id
		n.Likes = int(c.Db().CreateCommand(`
			SELECT COUNT(*) FROM {{votes}} v
			WHERE v.target_id = :uid AND v.liked = 1
			AND NOT EXISTS (SELECT 1 FROM {{votes}} m WHERE m.voter_id = :uid AND m.target_id = v.voter_id)
			AND NOT EXISTS (SELECT 1 FROM {{blocks}} b WHERE (b.blocker_id = :uid AND b.blocked_id = v.voter_id) OR (b.blocker_id = v.voter_id AND b.blocked_id = :uid))
		`).BindValue(":uid", uid).MustInt())
		n.Unread = UnreadFn(uid)
	}
	c.SetValue("users.counts", n)
	return n
}
