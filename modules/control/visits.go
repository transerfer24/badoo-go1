package control

import (
	"strconv"
	"strings"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
VisitsController — гости: кто чьи анкеты смотрит (только чтение, приложение не задевает).

В visits одна строка на пару «кто → чью анкету» со временем последнего просмотра (повторный просмотр обновляет at),
поэтому «за период» — это сколько разных анкет человек открывал в последний раз в этот период.
*/
type VisitsController struct{ web.Controller }

func (c *VisitsController) Behaviors() []web.Behavior { return access("visits", "index") }

func (c *VisitsController) Init() { c.Layout = "control" }

func (c *VisitsController) ActionIndex() error {
	return c.Render("visits", web.M{"section": "visits"})
}

func (c *VisitsController) ActionGetSummary() error {
	p := db.P{":since": periodSince(c.Post("period"), "24h"), ":hour": since("1h")}
	sum := app.Db.CreateCommand(`
		SELECT COUNT(*) AS visits, COUNT(DISTINCT visitor_id) AS visitors, COUNT(DISTINCT visited_id) AS visited,
		       COALESCE(SUM(at >= :hour), 0) AS last_hour
		FROM {{visits}} WHERE at >= :since`, p).MustRow()
	top := app.Db.CreateCommand(`SELECT COUNT(*) AS n FROM {{visits}} WHERE at >= :since GROUP BY visitor_id ORDER BY n DESC LIMIT 1`, p).MustInt()
	return c.Json(web.M{"status": 200, "summary": sum, "top": top})
}

/*
Топ за период: mode=visitors — кто больше всех смотрит (сотни анкет за час — похоже на бота или парсер),
mode=visited — чьи анкеты смотрят чаще всего. Агрегат — в подзапросе, к нему пользователи.
*/
func (c *VisitsController) ActionGetTop() error {
	page, perPage := max(1, c.PostInt("page", 1)), 50
	col, other := "visitor_id", "visited_id"
	if c.Post("mode") == "visited" {
		col, other = "visited_id", "visitor_id"
	}
	p := db.P{":since": periodSince(c.Post("period"), "24h"), ":hour": since("1h")}
	where := ""
	if c.Post("banned") != "1" {
		where = " AND u.banned = 0"
	}
	from := `
		FROM (SELECT ` + col + ` AS uid, COUNT(*) AS n, COALESCE(SUM(at >= :hour), 0) AS n_hour, MAX(at) AS last_at
		      FROM {{visits}} WHERE at >= :since GROUP BY ` + col + `) s
		INNER JOIN {{users}} u ON u.id = s.uid
		WHERE 1 = 1` + where
	count := app.Db.CreateCommand("SELECT COUNT(*)"+from, p).MustInt()
	items := db.Rows{}
	if int64((page-1)*perPage) < count {
		items = app.Db.CreateCommand(`
			SELECT u.id, u.name, u.gender, u.birthdate, u.city, u.verified, u.banned, u.created_at, u.last_seen,
			       s.n, s.n_hour, s.last_at,
			       (SELECT COUNT(*) FROM {{visits}} t WHERE t.`+other+` = u.id) AS reverse_total,
			       (SELECT COUNT(*) FROM {{reports}} r WHERE r.reported_id = u.id) AS reports,
			       (SELECT ph.file FROM {{photos}} ph WHERE ph.user_id = u.id ORDER BY ph.pos, ph.id LIMIT 1) AS photo`+from+`
			ORDER BY s.n DESC, u.id DESC LIMIT `+strconv.Itoa(perPage)+` OFFSET `+strconv.Itoa((page-1)*perPage), p).MustAll()
	}
	return c.Json(web.M{"status": 200, "items": prepare(items), "count": count})
}

// Лента просмотров: свежие сверху; uid — его просмотры и просмотры его анкеты
func (c *VisitsController) ActionGetFeed() error {
	q := app.Db.Query().
		Select("v.visitor_id", "v.visited_id", "v.at AS visited_at",
			"a.name AS visitor_name", "a.banned AS visitor_banned", "b.name AS target_name", "b.banned AS target_banned",
			"(SELECT ph.file FROM {{photos}} ph WHERE ph.user_id = v.visitor_id ORDER BY ph.pos, ph.id LIMIT 1) AS visitor_photo",
			"(SELECT ph.file FROM {{photos}} ph WHERE ph.user_id = v.visited_id ORDER BY ph.pos, ph.id LIMIT 1) AS target_photo").
		From("{{visits}} v").
		LeftJoin("{{users}} a", db.Raw("a.id = v.visitor_id")).
		LeftJoin("{{users}} b", db.Raw("b.id = v.visited_id"))
	if s := strings.TrimPrefix(strings.TrimSpace(c.Post("uid")), "#"); s != "" {
		uid, _ := strconv.ParseInt(s, 10, 64)
		switch c.Post("dir") {
		case "out":
			q.AndWhere(db.H{"v.visitor_id": uid})
		case "in":
			q.AndWhere(db.H{"v.visited_id": uid})
		default:
			q.AndWhere(db.Raw("(v.visitor_id = :uid OR v.visited_id = :uid)", db.P{":uid": uid}))
		}
	}
	if t := since(c.Post("period")); !t.IsZero() {
		q.AndWhere(db.Raw("v.at >= :since", db.P{":since": t}))
	}
	q.OrderBy("v.at DESC")
	p := mustPaginate(q, max(1, c.PostInt("page", 1)), 50)
	return c.Json(web.M{"status": 200, "items": prepare(p.Items), "count": p.Total})
}
