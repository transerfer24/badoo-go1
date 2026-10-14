// Package discovery — подбор анкет, голоса, мэтчи, блокировки (перенос handlers_discovery.go без транспорта).
//
// Фильтр никогда не расширяется сам: если подходящих нет — пустое состояние, а не «похожие».
package discovery

import (
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
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
	ExcludeVoted   bool
	Limit          int
}

func DefaultFilter(me *users.User) Filter {
	return Filter{AgeMin: me.AgeMin, AgeMax: me.AgeMax, MaxKm: me.MaxKm, Seeking: me.Seeking, VerifiedOnly: me.VerifiedOnly}
}

// Candidates — анкеты под фильтр (без фото: из 500 кандидатов на экран попадёт 12–90, их дозагрузит вызывающий).
func Candidates(me *users.User, f Filter) []*users.User {
	if f.MaxKm <= 0 {
		f.MaxKm = 50
	}
	dLat := float64(f.MaxKm) / 111.0
	dLng := float64(f.MaxKm) / (111.0 * math.Max(0.2, math.Cos(me.Lat*math.Pi/180)))
	now := time.Now()
	oldest := now.AddDate(-(f.AgeMax + 1), 0, 1).Format("2006-01-02")
	youngest := now.AddDate(-f.AgeMin, 0, 0).Format("2006-01-02")

	sql := `SELECT u.* FROM {{users}} u
		WHERE u.onboarded = 1 AND u.id <> :me
		  AND u.lat BETWEEN :lat1 AND :lat2 AND u.lng BETWEEN :lng1 AND :lng2
		  AND u.birthdate BETWEEN :oldest AND :youngest
		  AND NOT EXISTS (SELECT 1 FROM {{blocks}} b WHERE (b.blocker_id = :me AND b.blocked_id = u.id) OR (b.blocker_id = u.id AND b.blocked_id = :me))
		  AND (u.invisible = 0 OR EXISTS (SELECT 1 FROM {{votes}} lv WHERE lv.voter_id = u.id AND lv.target_id = :me AND lv.liked = 1))`
	params := db.P{":me": me.Id, ":lat1": me.Lat - dLat, ":lat2": me.Lat + dLat, ":lng1": me.Lng - dLng, ":lng2": me.Lng + dLng, ":oldest": oldest, ":youngest": youngest}
	if f.Seeking == "m" || f.Seeking == "f" {
		sql += ` AND u.gender = :gender`
		params[":gender"] = f.Seeking
	}
	if f.VerifiedOnly {
		sql += ` AND u.verified = 2`
	}
	if f.OnlineOnly {
		sql += ` AND u.last_seen > :online AND u.invisible = 0`
		params[":online"] = now.Add(-5 * time.Minute).UTC()
	}
	if f.ExcludeVoted {
		sql += ` AND NOT EXISTS (SELECT 1 FROM {{votes}} v WHERE v.voter_id = :me AND v.target_id = u.id)`
	}
	lim := 500
	if f.Limit > 0 {
		lim = f.Limit
	}
	rows := app.Db.CreateCommand(sql+" LIMIT "+itoa(lim), params).MustAll()

	out := []*users.User{}
	for _, r := range rows {
		u := users.Scan(r)
		// пол/поиск должны совпасть в обе стороны
		tmp := *me
		tmp.Seeking = f.Seeking
		if !Compatible(&tmp, u) {
			continue
		}
		u.DistKm = users.HaversineKm(me.Lat, me.Lng, u.Lat, u.Lng)
		if u.DistKm <= float64(f.MaxKm) {
			out = append(out, u)
		}
	}
	return out
}

func itoa(n int) string { return fmtInt(n) }

// Взаимно ли совместимы двое по полу/поиску.
func Compatible(me, other *users.User) bool {
	okMe := me.Seeking == "any" || me.Seeking == other.Gender
	okOther := other.Seeking == "any" || other.Seeking == me.Gender
	return okMe && okOther
}

// Rank — ближе, активнее и тех, кто уже лайкнул, — выше; немного случайности, чтобы лента не была одинаковой.
func Rank(me *users.User, list []*users.User, maxKm int) {
	likedMe := map[int64]bool{}
	for _, v := range app.Db.CreateCommand("SELECT voter_id FROM {{votes}} WHERE target_id = :me AND liked = 1").BindValue(":me", me.Id).MustColumn() {
		likedMe[toInt64(v)] = true
	}
	// у кого есть фото — одним запросом по индексу photos_user (сами фото не тянем)
	withPhoto := map[int64]bool{}
	if len(list) > 0 {
		ids := make([]int64, len(list))
		for i, u := range list {
			ids[i] = u.Id
		}
		for _, v := range app.Db.Query().Select("user_id").Distinct().From("{{photos}}").Where(db.In("user_id", ids)).MustColumn() {
			withPhoto[toInt64(v)] = true
		}
	}
	score := map[int64]float64{}
	for _, u := range list {
		s := rand.Float64()*2 - u.DistKm/float64(maxKm)
		if likedMe[u.Id] {
			s += 1.2
		}
		if u.Online() {
			s += 0.4
		}
		if withPhoto[u.Id] {
			s += 0.3
		}
		score[u.Id] = s
	}
	sort.Slice(list, func(i, j int) bool { return score[list[i].Id] > score[list[j].Id] })
}

// Card — анкета для JSON (лента встреч, API v1). Поля — как в старом бэкенде, приложение на них рассчитывает.
type Card struct {
	Id        int64    `json:"id"`
	Name      string   `json:"name"`
	Age       int      `json:"age"`
	City      string   `json:"city"`
	Dist      string   `json:"dist"`
	Verified  bool     `json:"verified"`
	Online    bool     `json:"online"`
	Photos    []string `json:"photos"`
	Bio       string   `json:"bio"`
	Intent    string   `json:"intent"`
	Interests []string `json:"interests"`
	Height    int      `json:"height"`
}

func ToCard(u *users.User) Card {
	return Card{u.Id, u.Name, u.Age(), u.City, u.DistLabel(), u.IsVerified(), u.Online(), u.Gallery(), u.Bio, u.IntentLabel(), u.InterestList(), u.Height}
}

// ---------- Голоса и мэтчи ----------

type VoteResult struct {
	Match   bool   `json:"match"`
	MatchId int64  `json:"matchId,omitempty"`
	Name    string `json:"name,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
}

// Vote — «да»/«нет» анкете. ok=false — такой анкеты нет (своя, не заполнена, блокировка).
func Vote(me *users.User, target int64, liked bool) (VoteResult, bool) {
	var res VoteResult
	other := users.ById(target)
	if other == nil || other.Id == me.Id || !other.Onboarded || BlockedEither(me.Id, other.Id) {
		return res, false
	}
	upsert("{{votes}}", db.H{"voter_id": me.Id, "target_id": target, "liked": liked, "created_at": time.Now().UTC()},
		[]string{"liked", "created_at"}, "voter_id", "target_id")
	if !liked {
		return res, true
	}
	back := app.Db.CreateCommand("SELECT COUNT(*) FROM {{votes}} WHERE voter_id = :t AND target_id = :me AND liked = 1").
		BindValue(":t", target).BindValue(":me", me.Id).MustInt()
	if back == 0 {
		hub.Send(target, "like", map[string]any{})
		return res, true
	}
	p, q := Pair(me.Id, target)
	upsert("{{matches}}", db.H{"user_a": p, "user_b": q, "created_at": time.Now().UTC()}, nil, "user_a", "user_b")
	res = VoteResult{Match: true, MatchId: MatchId(me.Id, target), Name: other.Name, Avatar: other.Avatar()}
	hub.Send(target, "match", VoteResult{Match: true, MatchId: res.MatchId, Name: me.Name, Avatar: me.Avatar()})
	return res, true
}

// Undo — отмена последнего «нет» (Premium). nil — отменять нечего.
func Undo(me *users.User) *users.User {
	row := app.Db.CreateCommand("SELECT target_id FROM {{votes}} WHERE voter_id = :me AND liked = 0 ORDER BY created_at DESC LIMIT 1").
		BindValue(":me", me.Id).MustRow()
	if row == nil {
		return nil
	}
	target := row.Int64("target_id")
	app.Db.MustDelete("{{votes}}", db.H{"voter_id": me.Id, "target_id": target})
	u := users.ById(target)
	if u == nil {
		return nil
	}
	u.DistKm = users.HaversineKm(me.Lat, me.Lng, u.Lat, u.Lng)
	return u
}

func Pair(a, b int64) (int64, int64) {
	if a < b {
		return a, b
	}
	return b, a
}

func MatchId(x, y int64) int64 {
	p, q := Pair(x, y)
	return app.Db.CreateCommand("SELECT id FROM {{matches}} WHERE user_a = :a AND user_b = :b").BindValue(":a", p).BindValue(":b", q).MustInt()
}

func BlockedEither(x, y int64) bool {
	return app.Db.CreateCommand("SELECT COUNT(*) FROM {{blocks}} WHERE (blocker_id = :x AND blocked_id = :y) OR (blocker_id = :y AND blocked_id = :x)").
		BindValue(":x", x).BindValue(":y", y).MustInt() > 0
}

// BlockedSet — все, с кем у uid блокировка в любую сторону (один запрос вместо запроса на каждого).
func BlockedSet(uid int64) map[int64]bool {
	out := map[int64]bool{}
	for _, v := range app.Db.CreateCommand("SELECT blocked_id FROM {{blocks}} WHERE blocker_id = :u UNION SELECT blocker_id FROM {{blocks}} WHERE blocked_id = :u").BindValue(":u", uid).MustColumn() {
		out[toInt64(v)] = true
	}
	return out
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

// WithDistance — анкеты по SQL c фото и расстоянием до меня (было usersByQuery).
func WithDistance(me *users.User, sql string, params db.P) []*users.User {
	out := []*users.User{}
	for _, r := range app.Db.CreateCommand(sql, params).MustAll() {
		out = append(out, users.Scan(r))
	}
	users.FillPhotos(out)
	for _, u := range out {
		u.DistKm = users.HaversineKm(me.Lat, me.Lng, u.Lat, u.Lng)
	}
	return out
}

// NotBlocked — условие «нет блокировки между :me и u» для запросов с alias u.
const NotBlocked = ` AND NOT EXISTS (SELECT 1 FROM {{blocks}} b WHERE (b.blocker_id = :me AND b.blocked_id = u.id) OR (b.blocker_id = u.id AND b.blocked_id = :me))`

func toInt64(v any) int64 { return db.Row{"v": v}.Int64("v") }
func fmtInt(n int) string { return strconv.Itoa(n) }

// upsert — Upsert без err (как Must-методы ядра): INSERT OR REPLACE / OR IGNORE из SQLite-версии.
func upsert(table string, row db.H, updateCols []string, keys ...string) {
	if _, err := app.Db.Upsert(table, row, updateCols, keys...); err != nil {
		panic(db.MustError{Err: err})
	}
}
