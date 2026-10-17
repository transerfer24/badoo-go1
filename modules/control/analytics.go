package control

import (
	"strings"
	"time"

	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
AnalyticsController — воронка и удержание (только чтение, приложение не задевает).

  - Воронка — по когорте «зарегистрировались в период»: анкета → фото → поставил лайк → получил лайк → пара →
    написал → переписка в обе стороны. Каждый шаг — «хоть раз было» за всё время, а не только в период.
  - Удержание — недельные когорты регистраций: какая доля была в сети позже, чем через 1/7/14/30 дней после
    регистрации. Истории визитов нет, поэтому меряем по last_seen — «вернулся хотя бы раз после N-го дня».
  - Активность за период — голоса, взаимность лайков, пары, сообщения; по часам (UTC, в JS — в поясе браузера).
*/
type AnalyticsController struct{ web.Controller }

func (c *AnalyticsController) Behaviors() []web.Behavior { return access("analytics", "index") }

func (c *AnalyticsController) Init() { c.Layout = "control" }

func (c *AnalyticsController) ActionIndex() error {
	return c.Render("analytics", web.M{"section": "analytics", "cities": users.Cities})
}

// period — from/to из фильтра (YYYY-MM-DD, to включительно), по умолчанию — последние 30 дней.
func (c *AnalyticsController) period() (from, to time.Time) {
	now := time.Now().UTC()
	to = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	from = to.AddDate(0, 0, -30)
	if t, ok := parseDay(c.Post("from")); ok {
		from = t
	}
	if t, ok := parseDay(c.Post("to")); ok {
		to = t.AddDate(0, 0, 1)
	}
	if !to.After(from) {
		to = from.AddDate(0, 0, 1)
	}
	return from, to
}

// cohortWhere — пол и город когорты (необязательные)
func (c *AnalyticsController) cohortWhere(p db.P) string {
	w := ""
	if g := c.Post("gender"); g == "m" || g == "f" || g == "x" {
		w += " AND u.gender = :gender"
		p[":gender"] = g
	}
	if city := strings.TrimSpace(c.Post("city")); city != "" {
		w += " AND u.city = :city"
		p[":city"] = city
	}
	return w
}

func (c *AnalyticsController) ActionGetFunnel() error {
	from, to := c.period()
	p := db.P{":from": from, ":to": to}
	where := c.cohortWhere(p)

	funnel := app.Db.CreateCommand(`
		SELECT COUNT(*) AS registered,
		       COALESCE(SUM(u.onboarded = 1), 0) AS onboarded,
		       COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM {{photos}} ph WHERE ph.user_id = u.id) THEN 1 ELSE 0 END), 0) AS photo,
		       COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM {{votes}} v WHERE v.voter_id = u.id AND v.liked = 1) THEN 1 ELSE 0 END), 0) AS liked,
		       COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM {{votes}} v WHERE v.target_id = u.id AND v.liked = 1) THEN 1 ELSE 0 END), 0) AS got_like,
		       COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM {{matches}} x WHERE x.user_a = u.id)
		                           OR EXISTS (SELECT 1 FROM {{matches}} x WHERE x.user_b = u.id) THEN 1 ELSE 0 END), 0) AS matched,
		       COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM {{messages}} m WHERE m.sender_id = u.id) THEN 1 ELSE 0 END), 0) AS wrote,
		       COALESCE(SUM(CASE WHEN EXISTS (SELECT 1 FROM {{messages}} m WHERE m.sender_id = u.id)
		                          AND EXISTS (SELECT 1 FROM {{messages}} m INNER JOIN {{matches}} x ON x.id = m.match_id
		                                      WHERE (x.user_a = u.id OR x.user_b = u.id) AND m.sender_id <> u.id) THEN 1 ELSE 0 END), 0) AS dialog,
		       COALESCE(SUM(u.verified = 2), 0) AS verified,
		       COALESCE(SUM(CASE WHEN u.premium_until IS NOT NULL THEN 1 ELSE 0 END), 0) AS premium,
		       COALESCE(SUM(u.banned = 1), 0) AS banned
		FROM {{users}} u
		WHERE u.created_at >= :from AND u.created_at < :to`+where, p).MustRow()

	// активность за период (все пользователи, не только когорта)
	activity := app.Db.CreateCommand(`
		SELECT (SELECT COUNT(*) FROM {{votes}} WHERE created_at >= :from AND created_at < :to) AS votes,
		       (SELECT COUNT(*) FROM {{votes}} WHERE created_at >= :from AND created_at < :to AND liked = 1) AS likes,
		       (SELECT COUNT(*) FROM {{votes}} v WHERE v.created_at >= :from AND v.created_at < :to AND v.liked = 1
		           AND EXISTS (SELECT 1 FROM {{votes}} w WHERE w.voter_id = v.target_id AND w.target_id = v.voter_id AND w.liked = 1)) AS mutual,
		       (SELECT COUNT(DISTINCT voter_id) FROM {{votes}} WHERE created_at >= :from AND created_at < :to) AS voters,
		       (SELECT COUNT(*) FROM {{matches}} WHERE created_at >= :from AND created_at < :to) AS matches,
		       (SELECT COUNT(*) FROM {{messages}} WHERE created_at >= :from AND created_at < :to) AS messages,
		       (SELECT COUNT(DISTINCT sender_id) FROM {{messages}} WHERE created_at >= :from AND created_at < :to) AS writers,
		       (SELECT COUNT(DISTINCT match_id) FROM {{messages}} WHERE created_at >= :from AND created_at < :to) AS chats
	`, db.P{":from": from, ":to": to}).MustRow()

	// по часам суток (UTC): SUBSTR даты — одинаково в MySQL и SQLite
	hours := web.M{}
	for _, t := range []string{"votes", "messages", "matches"} {
		hours[t] = app.Db.CreateCommand(`SELECT SUBSTR(created_at, 12, 2) AS h, COUNT(*) AS n FROM {{`+t+`}}
			WHERE created_at >= :from AND created_at < :to GROUP BY SUBSTR(created_at, 12, 2)`, db.P{":from": from, ":to": to}).MustAll()
	}

	return c.Json(web.M{
		"status": 200, "funnel": funnel, "activity": activity, "hours": hours, "retention": c.retention(where, p),
		"from": from.Format("2006-01-02"), "to": to.AddDate(0, 0, -1).Format("2006-01-02"),
	})
}

// retentionDays — пороги удержания: «был в сети позже, чем через N дней после регистрации»
var retentionDays = []int{1, 7, 14, 30}

/*
retention — 8 недельных когорт (с понедельника), последняя — текущая неделя. Даты считаются в Go: интервалы
дат в MySQL и SQLite пишутся по-разному. ready[i] — сколько из когорты уже прожили N дней (доля — от них).
*/
func (c *AnalyticsController) retention(where string, p db.P) []web.M {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	start := monday.AddDate(0, 0, -7*7)

	q := db.P{":start": start}
	for k, v := range p {
		if k != ":from" && k != ":to" {
			q[k] = v
		}
	}
	rows := app.Db.CreateCommand(`SELECT u.created_at, u.last_seen FROM {{users}} u WHERE u.created_at >= :start`+where, q).MustAll()

	type cohort struct {
		total        int
		ready, alive []int
	}
	weeks := make([]cohort, 8)
	for i := range weeks {
		weeks[i] = cohort{ready: make([]int, len(retentionDays)), alive: make([]int, len(retentionDays))}
	}
	for _, r := range rows {
		created, seen := r.Time("created_at"), r.Time("last_seen")
		w := int(created.Sub(start).Hours() / (24 * 7))
		if w < 0 || w > 7 {
			continue
		}
		weeks[w].total++
		for i, d := range retentionDays {
			after := time.Duration(d) * 24 * time.Hour
			if now.Sub(created) < after {
				continue
			}
			weeks[w].ready[i]++
			if seen.Sub(created) >= after {
				weeks[w].alive[i]++
			}
		}
	}
	out := []web.M{}
	for i := len(weeks) - 1; i >= 0; i-- {
		w := weeks[i]
		out = append(out, web.M{"week": start.AddDate(0, 0, 7*i).Format("2006-01-02"), "total": w.total, "ready": w.ready, "alive": w.alive})
	}
	return out
}
