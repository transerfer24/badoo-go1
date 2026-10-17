// Package meetups — события («встречи этой недели»): живые групповые мероприятия рядом (перенос backend/meetups.go).
// Человек отмечает «Пойду», видит, кто ещё идёт, на месте отмечается «Я здесь», а потом лайкает тех,
// с кем был на одном событии. В API это /api/v1/meetups — /api/v1/events занят лентой обновлений.
//
// Отличие от SQLite-версии: даты — DATETIME (time.Time), «не отметился» — NULL в checked_in_at, лица участников
// режутся до 6 на событие в Go, а не оконной функцией (ROW_NUMBER нет в MySQL 5.7).
package meetups

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"badoo/components/photos"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/helpers/arr"
)

type Kind struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Icon  string `json:"icon"` // имя иконки Ionicons в приложении
}

var Kinds = []Kind{
	{"drinks", "Бар и вечеринки", "wine-outline"},
	{"beach", "Пляж", "sunny-outline"},
	{"sport", "Спорт", "bicycle-outline"},
	{"food", "Еда", "restaurant-outline"},
	{"culture", "Культура", "color-palette-outline"},
	{"language", "Языковой обмен", "chatbubbles-outline"},
	{"games", "Квизы и игры", "dice-outline"},
	{"walk", "Прогулка", "walk-outline"},
}

func KindByKey(k string) (Kind, bool) {
	for _, m := range Kinds {
		if m.Key == k {
			return m, true
		}
	}
	return Kind{}, false
}

// Отметиться «Я здесь» можно за час до начала и до конца события.
const CheckinEarly = time.Hour

// Meetup — строка таблицы meetups.
type Meetup struct {
	Id       int64     `db:"id"`
	Title    string    `db:"title"`
	About    string    `db:"about"`
	Kind     string    `db:"kind"`
	Place    string    `db:"place"`
	Address  string    `db:"address"`
	City     string    `db:"city"`
	Lat      float64   `db:"lat"`
	Lng      float64   `db:"lng"`
	StartsAt time.Time `db:"starts_at"`
	EndsAt   time.Time `db:"ends_at"`
	Capacity int       `db:"capacity"`
	Price    string    `db:"price"`
	HostId   int64     `db:"host_id"` // 0 — без организатора (тестовое)
	Demo     bool      `db:"demo"`
	// организатор — JOIN users h (sel), чтобы не ходить за ним отдельно
	HostName  string `db:"host_name"`
	HostPhoto string `db:"host_photo"`
}

// sel — колонки события с организатором; FROM {{meetups}} m LEFT JOIN {{users}} h ON h.id = m.host_id.
const sel = `SELECT m.*, h.name AS host_name, (SELECT p.file FROM {{photos}} p WHERE p.user_id = h.id ORDER BY p.pos, p.id LIMIT 1) AS host_photo
	FROM {{meetups}} m LEFT JOIN {{users}} h ON h.id = m.host_id`

func scan(r db.Row) *Meetup {
	m := &Meetup{}
	if err := r.Scan(m); err != nil {
		panic(db.MustError{Err: err})
	}
	return m
}

// ById — событие; nil — нет.
func ById(id int64) *Meetup {
	r := app.Db.CreateCommand(sel+" WHERE m.id = :id").BindValue(":id", id).MustRow()
	if r == nil {
		return nil
	}
	return scan(r)
}

type Face struct {
	Id    int64  `json:"id"`
	Name  string `json:"name"`
	Photo string `json:"photo"`
	Color int    `json:"color"`
}

type DTO struct {
	Id        int64   `json:"id"`
	Title     string  `json:"title"`
	About     string  `json:"about"`
	Kind      string  `json:"kind"`
	KindLabel string  `json:"kindLabel"`
	Icon      string  `json:"icon"`
	Place     string  `json:"place"`
	Address   string  `json:"address"`
	City      string  `json:"city"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	StartsAt  string  `json:"startsAt"`
	EndsAt    string  `json:"endsAt"`
	Capacity  int     `json:"capacity"`
	Price     string  `json:"price"`
	Dist      string  `json:"dist"`
	Going     int     `json:"going"`
	IGo       bool    `json:"iGo"`
	CheckedIn bool    `json:"checkedIn"`
	CanCheck  bool    `json:"canCheckIn"`
	Live      bool    `json:"live"` // идёт прямо сейчас
	IsHost    bool    `json:"isHost"`
	Host      *Face   `json:"host"`
	Faces     []Face  `json:"faces"`
}

// goingNotBlocked — исключить тех, с кем у :me блокировка (в любую сторону). Колонка — g.user_id.
// Заблокированные админкой (users.banned) — тоже.
const goingNotBlocked = ` AND g.user_id NOT IN (SELECT blocked_id FROM {{blocks}} WHERE blocker_id = :me UNION SELECT blocker_id FROM {{blocks}} WHERE blocked_id = :me UNION SELECT id FROM {{users}} WHERE banned = 1)`

// DTOs собирает карточки событий пачкой: на любой длины список — 4 запроса (кто сколько идёт, иду ли я,
// лица участников, организаторы), а не 4 на каждое событие.
func DTOs(me *users.User, list []*Meetup, now time.Time) []DTO {
	out := make([]DTO, 0, len(list))
	if len(list) == 0 {
		return out
	}
	ids := arr.Map(list, func(m *Meetup) int64 { return m.Id })
	p := db.P{":me": me.Id, ":ids": ids}

	going := map[int64]int{}
	for _, r := range app.Db.CreateCommand(`SELECT g.meetup_id, COUNT(*) AS n FROM {{meetup_going}} g WHERE g.meetup_id IN (:ids)`+goingNotBlocked+` GROUP BY g.meetup_id`, p).MustAll() {
		going[r.Int64("meetup_id")] = r.Int("n")
	}
	mine := map[int64]bool{} // событие → отметился ли (есть в map — идёт)
	for _, r := range app.Db.CreateCommand(`SELECT meetup_id, checked_in_at FROM {{meetup_going}} WHERE user_id = :me AND meetup_id IN (:ids)`, p).MustAll() {
		mine[r.Int64("meetup_id")] = !r.IsNull("checked_in_at")
	}
	// лица: я первым, потом отметившиеся, потом по времени записи; до 6 на событие
	faces := map[int64][]Face{}
	var faceIds []int64
	for _, r := range app.Db.CreateCommand(`SELECT g.meetup_id, u.id, u.name FROM {{meetup_going}} g INNER JOIN {{users}} u ON u.id = g.user_id
		WHERE g.meetup_id IN (:ids)`+goingNotBlocked+`
		ORDER BY g.meetup_id, CASE WHEN g.user_id = :me THEN 0 ELSE 1 END, g.checked_in_at DESC, g.created_at LIMIT 3000`, p).MustAll() {
		mid := r.Int64("meetup_id")
		if len(faces[mid]) >= 6 {
			continue
		}
		f := Face{Id: r.Int64("id"), Name: r.Str("name"), Color: users.ColorIndex(r.Int64("id"))}
		faces[mid] = append(faces[mid], f)
		faceIds = append(faceIds, f.Id)
	}
	if photo := firstPhotos(faceIds); len(photo) > 0 {
		for mid, fs := range faces {
			for i := range fs {
				fs[i].Photo = photo[fs[i].Id]
			}
			faces[mid] = fs
		}
	}
	for _, m := range list {
		k, _ := KindByKey(m.Kind)
		d := DTO{Id: m.Id, Title: m.Title, About: m.About, Kind: m.Kind, KindLabel: k.Label, Icon: k.Icon,
			Place: m.Place, Address: m.Address, City: m.City, Lat: m.Lat, Lng: m.Lng,
			StartsAt: users.Ts(m.StartsAt), EndsAt: users.Ts(m.EndsAt), Capacity: m.Capacity, Price: m.Price, Going: going[m.Id]}
		d.Dist = users.DistLabelKm(users.HaversineKm(me.Lat, me.Lng, m.Lat, m.Lng))
		if checked, ok := mine[m.Id]; ok {
			d.IGo, d.CheckedIn = true, checked
		}
		d.Live = now.After(m.StartsAt) && now.Before(m.EndsAt)
		d.CanCheck = d.IGo && !d.CheckedIn && now.After(m.StartsAt.Add(-CheckinEarly)) && now.Before(m.EndsAt)
		if m.HostId != 0 {
			d.IsHost = m.HostId == me.Id
			f := Face{Id: m.HostId, Name: m.HostName, Color: users.ColorIndex(m.HostId)}
			if m.HostPhoto != "" {
				f.Photo = photos.Url(m.HostPhoto)
			}
			d.Host = &f
		}
		d.Faces = faces[m.Id]
		if d.Faces == nil {
			d.Faces = []Face{}
		}
		out = append(out, d)
	}
	return out
}

func One(me *users.User, m *Meetup, now time.Time) DTO { return DTOs(me, []*Meetup{m}, now)[0] }

// firstPhotos — главное фото по пользователям (uid → url).
func firstPhotos(ids []int64) map[int64]string {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out
	}
	rows := app.Db.Query().Select("user_id", "file").From("{{photos}}").Where(db.In("user_id", ids)).OrderBy("user_id", "pos", "id").MustAll()
	for _, r := range rows {
		uid := r.Int64("user_id")
		if _, ok := out[uid]; !ok {
			out[uid] = photos.Url(r.Str("file"))
		}
	}
	return out
}

// Nearby — события рядом на ближайшие days дней (по радиусу из настроек, минимум 30 км).
func Nearby(me *users.User, days int, now time.Time) []*Meetup {
	days = max(1, min(days, 30))
	km := float64(max(me.MaxKm, 30))
	// события заблокированного организатора не показываем
	q := sel + ` WHERE m.ends_at > :now AND m.starts_at < :until AND (h.id IS NULL OR h.banned = 0)`
	p := db.P{":now": now, ":until": now.Add(time.Duration(days) * 24 * time.Hour)}
	if me.Lat != 0 || me.Lng != 0 {
		// грубая рамка по координатам прямо в базе, точное расстояние — ниже
		dLat := km / 111.0
		dLng := km / (111.0 * math.Max(0.2, math.Cos(me.Lat*math.Pi/180)))
		q += ` AND m.lat BETWEEN :lat1 AND :lat2 AND m.lng BETWEEN :lng1 AND :lng2`
		p[":lat1"], p[":lat2"], p[":lng1"], p[":lng2"] = me.Lat-dLat, me.Lat+dLat, me.Lng-dLng, me.Lng+dLng
	}
	list := []*Meetup{}
	for _, r := range app.Db.CreateCommand(q+` ORDER BY m.starts_at LIMIT 300`, p).MustAll() {
		m := scan(r)
		if me.Lat == 0 || users.HaversineKm(me.Lat, me.Lng, m.Lat, m.Lng) <= km {
			list = append(list, m)
		}
	}
	return list
}

// Attendees — кто идёт (кроме меня и заблокированных), отметившиеся первыми. Одним запросом: анкета (users.Cols),
// фото, расстояние, отметка (checked_in), мой голос (my_vote) и пара (match_id) — как $select/$join в WealthBook.
func Attendees(me *users.User, m *Meetup) db.Rows {
	p := users.P(me)
	p[":mid"] = m.Id
	return app.Db.CreateCommand(`SELECT `+users.Cols+`, `+users.Photo+`, `+users.Photos+`, `+users.Dist+`, `+users.MyVote+`, `+users.MatchId+`,
		  g.checked_in_at IS NOT NULL AS checked_in
		FROM {{meetup_going}} g INNER JOIN {{users}} u ON u.id = g.user_id
		WHERE g.meetup_id = :mid AND g.user_id <> :me AND u.onboarded = 1`+goingNotBlocked+`
		ORDER BY g.checked_in_at DESC, g.created_at`, p).MustAll()
}

// ---------- Создание и участие ----------

type Input struct {
	Title    string  `json:"title"`
	About    string  `json:"about"`
	Kind     string  `json:"kind"`
	Place    string  `json:"place"`
	Address  string  `json:"address"`
	StartsAt string  `json:"startsAt"` // RFC3339 с часовым поясом устройства
	Hours    float64 `json:"hours"`
	Capacity int     `json:"capacity"`
	Price    string  `json:"price"`
}

// Check — валидация формы; поле и текст ошибки, как ждёт приложение.
func (in *Input) Check(now time.Time) (time.Time, string, string) {
	in.Title, in.About = strings.TrimSpace(in.Title), strings.TrimSpace(in.About)
	in.Place, in.Address, in.Price = strings.TrimSpace(in.Place), strings.TrimSpace(in.Address), strings.TrimSpace(in.Price)
	if n := utf8.RuneCountInString(in.Title); n < 3 || n > 80 {
		return time.Time{}, "title", "Название — от 3 до 80 символов."
	}
	if _, ok := KindByKey(in.Kind); !ok {
		return time.Time{}, "kind", "Выберите, что это за событие."
	}
	if n := utf8.RuneCountInString(in.Place); n < 2 || n > 80 {
		return time.Time{}, "place", "Укажите место — например, «Пляж Эль-Куры, у спасателей»."
	}
	if utf8.RuneCountInString(in.About) > 1000 || utf8.RuneCountInString(in.Address) > 120 || utf8.RuneCountInString(in.Price) > 40 {
		return time.Time{}, "about", "Слишком длинный текст."
	}
	start, err := time.Parse(time.RFC3339, in.StartsAt)
	if err != nil {
		return time.Time{}, "startsAt", "Выберите день и время."
	}
	if start.Before(now.Add(-10*time.Minute)) || start.After(now.Add(60*24*time.Hour)) {
		return time.Time{}, "startsAt", "Событие можно назначить с сегодняшнего дня и до двух месяцев вперёд."
	}
	if in.Hours == 0 {
		in.Hours = 2
	}
	if in.Hours < 0.5 || in.Hours > 12 {
		return time.Time{}, "hours", "Длительность — от 30 минут до 12 часов."
	}
	if in.Capacity < 0 || in.Capacity > 500 {
		return time.Time{}, "capacity", "Мест — от 2 до 500, или без ограничения."
	}
	if in.Capacity == 1 {
		in.Capacity = 2
	}
	return start.UTC(), "", ""
}

// Create — своё событие (организатор сразу в списке идущих). Ошибка — поле и текст.
func Create(me *users.User, in Input, now time.Time) (*Meetup, string, string) {
	start, field, msg := in.Check(now)
	if field != "" {
		return nil, field, msg
	}
	mine := app.Db.CreateCommand("SELECT COUNT(*) FROM {{meetups}} WHERE host_id = :me AND ends_at > :now", db.P{":me": me.Id, ":now": now}).MustInt()
	if mine >= 5 {
		return nil, "title", "У вас уже 5 предстоящих событий — это максимум."
	}
	id := app.Db.MustInsert("{{meetups}}", db.H{
		"title": in.Title, "about": in.About, "kind": in.Kind, "place": in.Place, "address": in.Address, "city": me.City,
		"lat": me.Lat, "lng": me.Lng, "starts_at": start, "ends_at": start.Add(time.Duration(in.Hours * float64(time.Hour))),
		"capacity": in.Capacity, "price": in.Price, "host_id": me.Id, "demo": false, "created_at": now,
	})
	going(id, me.Id, now)
	return ById(id), "", ""
}

func going(mid, uid int64, now time.Time) {
	if _, err := app.Db.Upsert("{{meetup_going}}", db.H{"meetup_id": mid, "user_id": uid, "created_at": now}, nil, "meetup_id", "user_id"); err != nil {
		panic(db.MustError{Err: err})
	}
}

// Delete — организатор отменяет своё событие; false — не организатор.
func Delete(me *users.User, id int64) bool {
	if app.Db.MustDelete("{{meetups}}", db.H{"id": id, "host_id": me.Id}) == 0 {
		return false
	}
	app.Db.MustDelete("{{meetup_going}}", db.H{"meetup_id": id})
	return true
}

// Join — «Пойду»; код ошибки: ended | full.
func Join(me *users.User, m *Meetup, now time.Time) (string, string) {
	if !m.EndsAt.After(now) {
		return "ended", "Событие уже закончилось."
	}
	n := app.Db.CreateCommand("SELECT COUNT(*) FROM {{meetup_going}} WHERE meetup_id = :mid", db.P{":mid": m.Id}).MustInt()
	already := app.Db.CreateCommand("SELECT COUNT(*) FROM {{meetup_going}} WHERE meetup_id = :mid AND user_id = :me", db.P{":mid": m.Id, ":me": me.Id}).MustInt()
	if already == 0 && m.Capacity > 0 && int(n) >= m.Capacity {
		return "full", "Мест больше нет."
	}
	going(m.Id, me.Id, now)
	return "", ""
}

// Leave — «Не пойду»; код ошибки: ended | host.
func Leave(me *users.User, m *Meetup, now time.Time) (string, string) {
	if !m.EndsAt.After(now) {
		return "ended", "Событие уже закончилось."
	}
	if m.HostId == me.Id {
		return "host", "Вы организатор — событие можно только отменить."
	}
	app.Db.MustDelete("{{meetup_going}}", db.H{"meetup_id": m.Id, "user_id": me.Id})
	return "", ""
}

// Checkin — «Я на месте»; код ошибки: time | not_going.
func Checkin(me *users.User, m *Meetup, now time.Time) (string, string) {
	if now.Before(m.StartsAt.Add(-CheckinEarly)) || !now.Before(m.EndsAt) {
		return "time", "Отметиться можно за час до начала и до конца события."
	}
	n := app.Db.CreateCommand("UPDATE {{meetup_going}} SET checked_in_at = :now WHERE meetup_id = :mid AND user_id = :me AND checked_in_at IS NULL",
		db.P{":now": now, ":mid": m.Id, ":me": me.Id}).MustExecute()
	if n == 0 {
		if app.Db.CreateCommand("SELECT COUNT(*) FROM {{meetup_going}} WHERE meetup_id = :mid AND user_id = :me", db.P{":mid": m.Id, ":me": me.Id}).MustInt() == 0 {
			return "not_going", "Сначала нажмите «Пойду»."
		}
	}
	return "", ""
}

// Ref — событие в чужой анкете.
type Ref struct {
	Id       int64  `json:"id"`
	Title    string `json:"title"`
	StartsAt string `json:"startsAt"`
	Icon     string `json:"icon"`
	Both     bool   `json:"both"` // зритель тоже идёт
}

// OfUser — ближайшие события, на которые идёт человек (для его анкеты), общие со зрителем — первыми.
func OfUser(meId, uid int64) []Ref {
	out := []Ref{}
	rows := app.Db.CreateCommand(`SELECT m.id, m.title, m.starts_at, m.kind,
		EXISTS(SELECT 1 FROM {{meetup_going}} x WHERE x.meetup_id = m.id AND x.user_id = :me) AS both
		FROM {{meetup_going}} g INNER JOIN {{meetups}} m ON m.id = g.meetup_id
		WHERE g.user_id = :uid AND m.ends_at > :now ORDER BY both DESC, m.starts_at LIMIT 3`,
		db.P{":me": meId, ":uid": uid, ":now": time.Now().UTC()}).MustAll()
	for _, r := range rows {
		k, _ := KindByKey(r.Str("kind"))
		out = append(out, Ref{Id: r.Int64("id"), Title: r.Str("title"), StartsAt: users.Ts(r.Time("starts_at")), Icon: k.Icon, Both: r.Bool("both")})
	}
	return out
}
