// Package discovery — знакомства: колода встреч, «рядом», чужая анкета, голоса/мэтчи, блокировки, гости.
//
// Стиль — WealthBook: одна выборка = один SQL с JOIN, результат db.Rows сразу во вьюху или в JSON.
// Расстояние, главное фото, «лайкнул меня», мой голос — колонки запроса (users.Dist, users.Photo, users.LikedMe,
// users.MyVote), не вычисления в Go. Фильтр собирается как $where[] в контроллерах WealthBook.
package discovery

import (
	"math"
	"time"

	"badoo/components/hub"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

type Filter struct {
	AgeMin, AgeMax int
	MaxKm          int
	Seeking        string
	VerifiedOnly   bool
	OnlineOnly     bool
}

func DefaultFilter(me *users.User) Filter {
	return Filter{AgeMin: me.AgeMin, AgeMax: me.AgeMax, MaxKm: me.MaxKm, Seeking: me.Seeking, VerifiedOnly: me.VerifiedOnly}
}

// where — условия отбора кандидатов под фильтр: заполненная анкета, не я, возраст, пол в обе стороны, рамка координат,
// нет блокировки, невидимку видно только если она меня лайкнула. Точное расстояние — снаружи, по dist_km.
func where(me *users.User, f Filter) (string, db.P) {
	if f.MaxKm <= 0 {
		f.MaxKm = 50
	}
	dLat := float64(f.MaxKm) / 111.0
	dLng := float64(f.MaxKm) / (111.0 * math.Max(0.2, math.Cos(me.Lat*math.Pi/180)))
	now := time.Now()
	p := users.P(me)
	p[":maxkm"], p[":seeking"], p[":mygender"] = f.MaxKm, f.Seeking, me.Gender
	p[":lat1"], p[":lat2"], p[":lng1"], p[":lng2"] = me.Lat-dLat, me.Lat+dLat, me.Lng-dLng, me.Lng+dLng
	p[":oldest"], p[":youngest"] = now.AddDate(-(f.AgeMax+1), 0, 1).Format("2006-01-02"), now.AddDate(-f.AgeMin, 0, 0).Format("2006-01-02")

	sql := ` WHERE u.onboarded = 1 AND u.id <> :me
		AND u.lat BETWEEN :lat1 AND :lat2 AND u.lng BETWEEN :lng1 AND :lng2
		AND u.birthdate BETWEEN :oldest AND :youngest
		AND (:seeking = 'any' OR u.gender = :seeking) AND (u.seeking = 'any' OR u.seeking = :mygender)
		AND (u.invisible = 0 OR EXISTS (SELECT 1 FROM {{votes}} lv WHERE lv.voter_id = u.id AND lv.target_id = :me AND lv.liked = 1))` +
		users.NotBlocked
	if f.VerifiedOnly {
		sql += ` AND u.verified = 2`
	}
	if f.OnlineOnly {
		sql += ` AND u.last_seen > :online AND u.invisible = 0`
		p[":online"] = now.Add(-5 * time.Minute).UTC()
	}
	return sql, p
}

// candidates — подзапрос с полными колонками карточки; снаружи — фильтр по dist_km, сортировка и LIMIT.
const candidates = `SELECT ` + users.Cols + `, ` + users.Photo + `, ` + users.Photos + `, ` + users.Dist + `, ` + users.LikedMe + `
	FROM {{users}} u`

// Encounters — колода «Встреч»: не голосовал, в радиусе; ближе, активнее и лайкнувшие меня — выше, с долей случайности.
func Encounters(me *users.User) db.Rows {
	w, p := where(me, DefaultFilter(me))
	p[":recent"] = time.Now().Add(-5 * time.Minute).UTC()
	return app.Db.CreateCommand(`SELECT * FROM (`+candidates+w+`
		AND NOT EXISTS (SELECT 1 FROM {{votes}} v WHERE v.voter_id = :me AND v.target_id = u.id)) t
		WHERE t.dist_km <= :maxkm
		ORDER BY `+users.Rand()+` * 2 - t.dist_km / :maxkm + t.liked_me * 1.2
		  + CASE WHEN t.last_seen > :recent THEN 0.4 ELSE 0 END + CASE WHEN t.photo IS NULL THEN 0 ELSE 0.3 END DESC
		LIMIT 12`, p).MustAll()
}

// Nearby — «Рядом»: по расстоянию, до 90 анкет.
func Nearby(me *users.User, f Filter) db.Rows {
	w, p := where(me, f)
	return app.Db.CreateCommand(`SELECT * FROM (`+candidates+w+`) t WHERE t.dist_km <= :maxkm ORDER BY t.dist_km LIMIT 90`, p).MustAll()
}

// Profile — чужая (или своя) анкета со всем, что нужно экрану: фото, расстояние, лайкнул ли меня, мой голос, id пары.
// nil — нет, не заполнена или блокировка.
func Profile(me *users.User, id int64) db.Row {
	p := users.P(me)
	p[":id"] = id
	return app.Db.CreateCommand(`SELECT `+users.Cols+`, `+users.Photo+`, `+users.Photos+`, `+users.Dist+`, `+users.LikedMe+`, `+users.MyVote+`, `+users.MatchId+`
		FROM {{users}} u WHERE u.id = :id AND u.onboarded = 1`+users.NotBlocked, p).MustRow()
}

// Likers — кто лайкнул меня без ответа; Matches — взаимные. Свежие сверху.
func Likers(me *users.User) db.Rows {
	return app.Db.CreateCommand(`SELECT `+users.Cols+`, `+users.Photo+`, `+users.Dist+`
		FROM {{votes}} v INNER JOIN {{users}} u ON u.id = v.voter_id
		WHERE v.target_id = :me AND v.liked = 1
		AND NOT EXISTS (SELECT 1 FROM {{votes}} m WHERE m.voter_id = :me AND m.target_id = v.voter_id)`+users.NotBlocked+`
		ORDER BY v.created_at DESC LIMIT 200`, users.P(me)).MustAll()
}

func Matches(me *users.User) db.Rows {
	return app.Db.CreateCommand(`SELECT `+users.Cols+`, `+users.Photo+`, `+users.Dist+`, x.id AS match_id
		FROM {{matches}} x INNER JOIN {{users}} u ON u.id = CASE WHEN x.user_a = :me THEN x.user_b ELSE x.user_a END
		WHERE (x.user_a = :me OR x.user_b = :me)`+users.NotBlocked+` ORDER BY x.created_at DESC LIMIT 200`, users.P(me)).MustAll()
}

// Visitors — кто открывал мою анкету за 30 дней (колонка at — когда), свежие сверху.
func Visitors(me *users.User) db.Rows {
	p := users.P(me)
	p[":since"] = time.Now().AddDate(0, 0, -30).UTC()
	return app.Db.CreateCommand(`SELECT `+users.Cols+`, `+users.Photo+`, `+users.Dist+`, v.at
		FROM {{visits}} v INNER JOIN {{users}} u ON u.id = v.visitor_id
		WHERE v.visited_id = :me AND v.at > :since`+users.NotBlocked+` ORDER BY v.at DESC LIMIT 100`, p).MustAll()
}

// ---------- Действия ----------

type VoteResult struct {
	Match   bool   `json:"match"`
	MatchId int64  `json:"matchId,omitempty"`
	Name    string `json:"name,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
}

// Vote — «да»/«нет» анкете. ok=false — такой анкеты нет (своя, не заполнена, блокировка).
func Vote(me *users.User, target int64, liked bool) (VoteResult, bool) {
	var res VoteResult
	other := Profile(me, target)
	if other == nil || target == me.Id {
		return res, false
	}
	upsert("{{votes}}", db.H{"voter_id": me.Id, "target_id": target, "liked": liked, "created_at": time.Now().UTC()},
		[]string{"liked", "created_at"}, "voter_id", "target_id")
	if !liked {
		return res, true
	}
	if !other.Bool("liked_me") {
		hub.Send(target, "like", map[string]any{})
		return res, true
	}
	a, b := Pair(me.Id, target)
	now := time.Now().UTC()
	upsert("{{matches}}", db.H{"user_a": a, "user_b": b, "created_at": now, "last_at": now}, nil, "user_a", "user_b") // last_at — сортировка чатов до первого сообщения
	res = VoteResult{Match: true, MatchId: MatchIdOf(me.Id, target), Name: other.Str("name"), Avatar: users.AvatarOf(other["photo"], target)}
	hub.Send(target, "match", VoteResult{Match: true, MatchId: res.MatchId, Name: me.Name, Avatar: me.Avatar()})
	return res, true
}

// Undo — отмена последнего «нет» (Premium): возвращает анкету обратно в колоду. nil — отменять нечего.
func Undo(me *users.User) db.Row {
	target := app.Db.CreateCommand("SELECT target_id FROM {{votes}} WHERE voter_id = :me AND liked = 0 ORDER BY created_at DESC LIMIT 1").
		BindValue(":me", me.Id).MustInt()
	if target == 0 {
		return nil
	}
	app.Db.MustDelete("{{votes}}", db.H{"voter_id": me.Id, "target_id": target})
	return Profile(me, target)
}

func Pair(a, b int64) (int64, int64) {
	if a < b {
		return a, b
	}
	return b, a
}

// MatchIdOf — id пары двоих (0 — нет).
func MatchIdOf(x, y int64) int64 {
	a, b := Pair(x, y)
	return app.Db.CreateCommand("SELECT id FROM {{matches}} WHERE user_a = :a AND user_b = :b").BindValue(":a", a).BindValue(":b", b).MustInt()
}

func BlockedEither(x, y int64) bool {
	return app.Db.CreateCommand("SELECT COUNT(*) FROM {{blocks}} WHERE (blocker_id = :x AND blocked_id = :y) OR (blocker_id = :y AND blocked_id = :x)").
		BindValue(":x", x).BindValue(":y", y).MustInt() > 0
}

func Block(me, other int64) {
	if me != other {
		upsert("{{blocks}}", db.H{"blocker_id": me, "blocked_id": other}, nil, "blocker_id", "blocked_id")
	}
}

var ReportReasons = []string{"Фейковая анкета", "Спам или реклама", "Оскорбления", "Неприемлемые фото", "Мошенничество", "Похоже, младше 18"}

// Report — жалоба; false — причина не из списка или жалоба на себя.
func Report(me, other int64, reason string, block bool) bool {
	valid := false
	for _, x := range ReportReasons {
		valid = valid || x == reason
	}
	if !valid || me == other {
		return false
	}
	app.Db.MustInsert("{{reports}}", db.H{"reporter_id": me, "reported_id": other, "reason": reason, "created_at": time.Now().UTC()})
	if block {
		Block(me, other)
	}
	return true
}

// Visit — отметка «был в гостях». Невидимка с Premium не оставляет след.
func Visit(me *users.User, other int64) {
	if me.Invisible && me.IsPremium() {
		return
	}
	upsert("{{visits}}", db.H{"visitor_id": me.Id, "visited_id": other, "at": time.Now().UTC()}, []string{"at"}, "visitor_id", "visited_id")
}

// upsert — Upsert без err (как Must-методы ядра): INSERT OR REPLACE / OR IGNORE из SQLite-версии.
func upsert(table string, row db.H, updateCols []string, keys ...string) {
	if _, err := app.Db.Upsert(table, row, updateCols, keys...); err != nil {
		panic(db.MustError{Err: err})
	}
}
