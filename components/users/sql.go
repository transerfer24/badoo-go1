package users

/*
Куски SQL для выборок анкет — как $select / $join в контроллерах WealthBook: один запрос с JOIN на экран,
MustAll() → строки сразу во вьюху или в JSON. Склейки в Go нет: всё, что нужно шаблону, считает база.

	rows := c.Db().CreateCommand(`SELECT `+users.Cols+`, `+users.Photo+`, `+users.Dist+`
		FROM {{votes}} v INNER JOIN {{users}} u ON u.id = v.voter_id
		WHERE v.target_id = :me`+users.NotBlocked+`
		ORDER BY v.created_at DESC`, users.P(me)).MustAll()
	return c.Render("likes", web.M{"likers": rows})

и в шаблоне {{range .likers}} {{.name}}, {{age .birthdate}} {{dist .dist_km}} {{avatar .photo .id}} {{end}}.
Alias таблицы users в запросе — всегда u, параметры :me / :lat / :lng — из users.P(me).
*/

import (
	"fmt"
	"strings"
	"time"

	"badoo/components/photos"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

// Cols — поля анкеты для списков и карточек (u.*, кроме служебных: email, pass_hash, auth_key, настройки фильтров).
const Cols = `u.id, u.name, u.birthdate, u.gender, u.seeking, u.city, u.lat, u.lng, u.bio, u.intent, u.interests, u.height,
	u.verified, u.last_seen, u.invisible, u.premium_until, u.onboarded`

// Photo — главное фото (ключ в хранилище; в шаблоне {{avatar .photo .id}}), Photos — все через запятую ({{gallery .photos .id}}).
const (
	Photo = `(SELECT p.file FROM {{photos}} p WHERE p.user_id = u.id ORDER BY p.pos, p.id LIMIT 1) AS photo`
	// ORDER BY внутри GROUP_CONCAT, а не вложенный подзапрос: MySQL до 8.0.14 не пускает u.id внутрь derived table
	Photos = `(SELECT GROUP_CONCAT(p.file ORDER BY p.pos, p.id) FROM {{photos}} p WHERE p.user_id = u.id) AS photos`
)

// Dist — расстояние до :lat/:lng в километрах (гаверсинус; ACOS/RADIANS есть и в MySQL, и в SQLite ядра).
// CASE вместо LEAST: у точки с теми же координатами косинус из-за округления чуть больше 1, ACOS дал бы NULL.
const Dist = `6371 * ACOS(CASE WHEN ` + cosDist + ` > 1 THEN 1 ELSE ` + cosDist + ` END) AS dist_km`

const cosDist = `(COS(RADIANS(:lat)) * COS(RADIANS(u.lat)) * COS(RADIANS(u.lng) - RADIANS(:lng)) + SIN(RADIANS(:lat)) * SIN(RADIANS(u.lat)))`

// NotBlocked — «нет блокировки между :me и u» (в любую сторону).
const NotBlocked = ` AND NOT EXISTS (SELECT 1 FROM {{blocks}} b WHERE (b.blocker_id = :me AND b.blocked_id = u.id) OR (b.blocker_id = u.id AND b.blocked_id = :me))`

// LikedMe — u уже лайкнул :me (1/0); MyVote — мой голос по u (NULL — не голосовал).
const (
	LikedMe = `EXISTS(SELECT 1 FROM {{votes}} lv WHERE lv.voter_id = u.id AND lv.target_id = :me AND lv.liked = 1) AS liked_me`
	MyVote  = `(SELECT mv.liked FROM {{votes}} mv WHERE mv.voter_id = :me AND mv.target_id = u.id) AS my_vote`
)

// MatchId — id пары :me и u (NULL — пары нет).
const MatchId = `(SELECT x.id FROM {{matches}} x WHERE (x.user_a = :me AND x.user_b = u.id) OR (x.user_a = u.id AND x.user_b = :me)) AS match_id`

// RowOf — анкета id со всеми колонками карточки (фото, расстояние до me) без условий на onboarded/блокировки:
// для своей анкеты (MeDTO) и служебных мест. nil — нет такой.
func RowOf(me *User, id int64) db.Row {
	p := P(me)
	p[":id"] = id
	return app.Db.CreateCommand(`SELECT `+Cols+`, `+Photo+`, `+Photos+`, `+Dist+` FROM {{users}} u WHERE u.id = :id`, p).MustRow()
}

// P — параметры, которые нужны этим кускам: :me, :lat, :lng. Свои добавляются поверх: p := users.P(me); p[":x"] = …
func P(me *User) db.P {
	return db.P{":me": me.Id, ":lat": me.Lat, ":lng": me.Lng}
}

// Rand — случайное число 0..1 для перемешивания в ORDER BY (у MySQL и SQLite разные функции).
func Rand() string {
	if app.Db.DriverName() == "sqlite" {
		return `(ABS(RANDOM()) % 1000000) / 1000000.0`
	}
	return `RAND()`
}

// ---------- Функции шаблонов — Functions:: из WealthBook: считают из колонок строки ----------

// Funcs — регистрируются в config: for name, fn := range users.Funcs { a.Server.Views.AddFunc(name, fn) }.
var Funcs = map[string]any{
	"age":       AgeOf,
	"online":    OnlineAt,
	"verified":  func(v any) bool { return db.Row{"v": v}.Int("v") == 2 },
	"premium":   PremiumAt,
	"avatar":    AvatarOf,
	"gallery":   GalleryOf,
	"dist":      DistLabelKm,
	"gender":    func(g string) string { return GenderLabels[g] },
	"intent":    func(k string) string { return IntentLabels[k] },
	"interests": SplitList,
}

// AgeOf — полных лет по дате рождения YYYY-MM-DD.
func AgeOf(birthdate string) int {
	b, err := time.Parse("2006-01-02", birthdate)
	if err != nil {
		return 0
	}
	return AgeAt(b, time.Now())
}

// OnlineAt — был в сети меньше 5 минут назад.
func OnlineAt(lastSeen any) bool {
	t := db.Row{"v": lastSeen}.Time("v")
	return !t.IsZero() && time.Since(t) < 5*time.Minute
}

// PremiumAt — подписка активна.
func PremiumAt(until any) bool {
	t := db.Row{"v": until}.Time("v")
	return !t.IsZero() && t.After(time.Now())
}

// AvatarOf — URL главного фото или сгенерированный аватар.
func AvatarOf(photo any, id any) string {
	p := db.Row{"v": photo}.Str("v")
	if p != "" {
		return photos.Url(p)
	}
	return fmt.Sprintf("/avatar/%d.svg", db.Row{"v": id}.Int64("v"))
}

// GalleryOf — все фото (колонка photos через запятую) или аватар.
func GalleryOf(list any, id any) []string {
	out := []string{}
	for _, f := range SplitList(db.Row{"v": list}.Str("v")) {
		out = append(out, photos.Url(f))
	}
	if len(out) == 0 {
		out = append(out, AvatarOf("", id))
	}
	return out
}

// DistLabelKm — «меньше 1 км», «3.4 км», «25 км».
func DistLabelKm(km any) string {
	d := db.Row{"v": km}.Float("v")
	switch {
	case d < 1:
		return "меньше 1 км"
	case d < 10:
		return fmt.Sprintf("%.1f км", d)
	default:
		return fmt.Sprintf("%.0f км", d)
	}
}

// SplitList — "a,b, c" → [a b c].
func SplitList(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
