package control

import (
	"time"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

// IndexController — дашборд: люди, активность, модерация, регистрации по дням, города, свежие анкеты и жалобы.
type IndexController struct{ web.Controller }

func (c *IndexController) Behaviors() []web.Behavior { return access("index", "index") }

func (c *IndexController) Init() { c.Layout = "control" }

func (c *IndexController) ActionIndex() error {
	return c.Render("index", web.M{"section": "index"})
}

// Сводка для дашборда
func (c *IndexController) ActionGetIndexInfo() error {
	res := web.M{}
	now := time.Now().UTC()
	p := db.P{
		":now": now, ":online": now.Add(-5 * time.Minute), ":day": now.Add(-24 * time.Hour),
		":week": now.Add(-7 * 24 * time.Hour), ":month": now.Add(-30 * 24 * time.Hour),
	}

	users := app.Db.CreateCommand(`
		SELECT COUNT(*) AS total,
		       COALESCE(SUM(onboarded = 1), 0) AS onboarded,
		       COALESCE(SUM(created_at >= :day), 0) AS new_day,
		       COALESCE(SUM(created_at >= :week), 0) AS new_week,
		       COALESCE(SUM(created_at >= :month), 0) AS new_month,
		       COALESCE(SUM(last_seen >= :online), 0) AS online,
		       COALESCE(SUM(last_seen >= :day), 0) AS dau,
		       COALESCE(SUM(last_seen >= :week), 0) AS wau,
		       COALESCE(SUM(last_seen >= :month), 0) AS mau,
		       COALESCE(SUM(banned = 1), 0) AS banned,
		       COALESCE(SUM(verified = 2), 0) AS verified,
		       COALESCE(SUM(verified = 1 AND banned = 0), 0) AS verify_queue,
		       COALESCE(SUM(premium_until > :now), 0) AS premium,
		       COALESCE(SUM(gender = 'm'), 0) AS men,
		       COALESCE(SUM(gender = 'f'), 0) AS women,
		       COALESCE(SUM(gender = 'x'), 0) AS other
		FROM {{users}}
	`, p).MustRow()

	activity := app.Db.CreateCommand(`
		SELECT (SELECT COUNT(*) FROM {{matches}}) AS matches,
		       (SELECT COUNT(*) FROM {{matches}} WHERE created_at >= :day) AS matches_day,
		       (SELECT COUNT(*) FROM {{messages}}) AS messages,
		       (SELECT COUNT(*) FROM {{messages}} WHERE created_at >= :day) AS messages_day,
		       (SELECT COUNT(*) FROM {{votes}} WHERE created_at >= :day AND liked = 1) AS likes_day,
		       (SELECT COUNT(*) FROM {{votes}} WHERE created_at >= :day AND liked = 0) AS passes_day,
		       (SELECT COUNT(*) FROM {{photos}}) AS photos,
		       (SELECT COUNT(*) FROM {{reports}} WHERE status = 0) AS reports_new,
		       (SELECT COUNT(*) FROM {{reports}} WHERE created_at >= :day) AS reports_day,
		       (SELECT COUNT(*) FROM {{meetups}} WHERE ends_at > :now) AS meetups_upcoming,
		       (SELECT COUNT(*) FROM {{meetup_going}} g INNER JOIN {{meetups}} m ON m.id = g.meetup_id WHERE m.ends_at > :now) AS meetups_going,
		       (SELECT COUNT(DISTINCT user_id) FROM {{push_tokens}}) AS push_users,
		       (SELECT COUNT(DISTINCT user_id) FROM {{sessions}} WHERE expires_at > :now) AS app_users
	`, p).MustRow()

	// регистрации и активность по дням за 30 дней (SUBSTR даты — одинаково в MySQL и SQLite)
	regs := app.Db.CreateCommand(`
		SELECT SUBSTR(created_at, 1, 10) AS day, COUNT(*) AS n FROM {{users}}
		WHERE created_at >= :month GROUP BY SUBSTR(created_at, 1, 10) ORDER BY day
	`, p).MustAll()
	msgs := app.Db.CreateCommand(`
		SELECT SUBSTR(created_at, 1, 10) AS day, COUNT(*) AS n FROM {{messages}}
		WHERE created_at >= :month GROUP BY SUBSTR(created_at, 1, 10) ORDER BY day
	`, p).MustAll()
	matches := app.Db.CreateCommand(`
		SELECT SUBSTR(created_at, 1, 10) AS day, COUNT(*) AS n FROM {{matches}}
		WHERE created_at >= :month GROUP BY SUBSTR(created_at, 1, 10) ORDER BY day
	`, p).MustAll()

	cities := app.Db.CreateCommand(`
		SELECT city, COUNT(*) AS n, COALESCE(SUM(last_seen >= :week), 0) AS active
		FROM {{users}} WHERE city <> '' AND banned = 0
		GROUP BY city ORDER BY n DESC LIMIT 10
	`, p).MustAll()

	last := app.Db.CreateCommand(`
		SELECT u.id, u.name, u.email, u.gender, u.birthdate, u.city, u.verified, u.onboarded, u.banned, u.created_at, u.last_seen,
		       (SELECT p.file FROM {{photos}} p WHERE p.user_id = u.id ORDER BY p.pos, p.id LIMIT 1) AS photo
		FROM {{users}} u ORDER BY u.id DESC LIMIT 10
	`).MustAll()

	reports := app.Db.CreateCommand(`
		SELECT r.id, r.reason, r.created_at, r.reporter_id, r.reported_id,
		       a.name AS reporter_name, b.name AS reported_name, b.banned AS reported_banned
		FROM {{reports}} r
		LEFT JOIN {{users}} a ON a.id = r.reporter_id
		LEFT JOIN {{users}} b ON b.id = r.reported_id
		WHERE r.status = 0 ORDER BY r.id DESC LIMIT 8
	`).MustAll()

	res["status"] = 200
	res["users"] = users
	res["activity"] = activity
	res["days"] = web.M{"regs": regs, "messages": msgs, "matches": matches}
	res["cities"] = cities
	res["last"] = prepare(last)
	res["reports"] = prepare(reports)
	res["dev"] = app.Params["amura.dev"]
	res["chat_verify"] = app.Params["amura.chat_verify"]
	return c.Json(res)
}
