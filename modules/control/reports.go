package control

import (
	"strconv"
	"strings"
	"time"

	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
ReportsController — жалобы пользователей (кнопка «Пожаловаться» в анкете и чате).

Статусы reports.status: 0 новая, 1 приняты меры (заблокирован), 2 отклонена. Решение по жалобе — сразу по всем
новым жалобам на этого человека: заблокировать (UsersController.ActionSetBan делает то же) или отклонить.
*/
type ReportsController struct{ web.Controller }

func (c *ReportsController) Behaviors() []web.Behavior { return access("reports", "index") }

func (c *ReportsController) Init() { c.Layout = "control" }

func (c *ReportsController) ActionIndex() error {
	return c.Render("reports", web.M{"section": "reports"})
}

func (c *ReportsController) ActionGetReports() error {
	res := web.M{}
	q := c.reportsQuery()

	p, err := paginate(q, max(1, c.PostInt("page", 1)), 50)
	if err != nil {
		app.Log.Error("control: жалобы", "err", err)
		res["status"] = 200
		res["items"] = db.Rows{}
		return c.Json(res)
	}
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	res["counts"] = app.Db.CreateCommand(`SELECT COALESCE(SUM(status = 0), 0) AS new, COALESCE(SUM(status = 1), 0) AS resolved,
		COALESCE(SUM(status = 2), 0) AS rejected FROM {{reports}}`).MustRow()
	return c.Json(res)
}

// reportsQuery — выборка жалоб с фильтрами из POST (общая для списка и выгрузки CSV)
func (c *ReportsController) reportsQuery() *db.Query {
	q := app.Db.Query().
		Select("r.id", "r.reason", "r.status", "r.created_at", "r.resolved_at",
			"r.reporter_id", "a.name AS reporter_name",
			"r.reported_id", "b.name AS reported_name", "b.banned AS reported_banned", "b.ban_reason", "b.verified AS reported_verified",
			"(SELECT p.file FROM {{photos}} p WHERE p.user_id = b.id ORDER BY p.pos, p.id LIMIT 1) AS reported_photo",
			"(SELECT COUNT(*) FROM {{reports}} r2 WHERE r2.reported_id = r.reported_id) AS reported_total",
			"(SELECT x.id FROM {{matches}} x WHERE (x.user_a = r.reporter_id AND x.user_b = r.reported_id) OR (x.user_a = r.reported_id AND x.user_b = r.reporter_id)) AS match_id",
			"m.name AS resolved_by_name").
		From("{{reports}} r").
		LeftJoin("{{users}} a", db.Raw("a.id = r.reporter_id")).
		LeftJoin("{{users}} b", db.Raw("b.id = r.reported_id")).
		LeftJoin("{{users}} m", db.Raw("m.id = r.resolved_by"))

	if st := c.Post("status", "0"); st == "0" || st == "1" || st == "2" {
		q.AndWhere(db.H{"r.status": st})
	}
	if s := strings.TrimSpace(c.Post("search")); s != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
		q.AndWhere(db.Raw("(r.reason LIKE :s OR a.name LIKE :s OR b.name LIKE :s OR r.reporter_id = :sid OR r.reported_id = :sid)",
			db.P{":s": "%" + s + "%", ":sid": id}))
	}
	if c.Post("sort") == "count" {
		q.OrderBy("reported_total DESC", "r.id DESC")
	} else {
		q.OrderBy("r.id DESC")
	}
	return q
}

// Выгрузка CSV по текущим фильтрам
func (c *ReportsController) ActionExport() error {
	return csvExport(c.Context, "reports", c.reportsQuery(), [][2]string{
		{"id", "Жалоба"}, {"created_at", "Когда"}, {"status", "Статус (0 новая, 1 меры, 2 отклонена)"}, {"reason", "Причина"},
		{"reported_id", "На кого #"}, {"reported_name", "На кого"}, {"reported_banned", "Заблокирован"}, {"reported_total", "Жалоб на него"},
		{"reporter_id", "От кого #"}, {"reporter_name", "От кого"}, {"resolved_by_name", "Решил"}, {"resolved_at", "Когда решено"},
	}, "status", "search", "sort")
}

// Решение: action = ban (заблокировать того, на кого жалуются) | reject (жалоба не подтвердилась).
// Применяется ко всем новым жалобам на этого пользователя.
func (c *ReportsController) ActionResolve() error {
	res := web.M{}
	rep := app.Db.CreateCommand("SELECT id, reported_id FROM {{reports}} WHERE id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if rep == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Жалоба не найдена"}
		return c.Json(res)
	}
	uid := rep.Int64("reported_id")
	now := time.Now().UTC()
	switch c.Post("action") {
	case "ban":
		if uid == c.User.Id || IsSuper(uid) || (IsAdmin(uid) && !IsSuper(c.User.Id)) {
			res["status"] = 500
			res["errors"] = web.M{"modal_message": "Этого пользователя заблокировать нельзя"}
			return c.Json(res)
		}
		reason := strings.TrimSpace(c.Post("reason"))
		if len([]rune(reason)) > 255 {
			reason = string([]rune(reason)[:255])
		}
		users.Ban(uid, c.User.Id, reason)
		n := app.Db.MustUpdate("{{reports}}", db.H{"status": 1, "resolved_by": c.User.Id, "resolved_at": now}, db.H{"reported_id": uid, "status": 0})
		logAction(c.Context, uid, "ban", reason+" (жалоб: "+strconv.FormatInt(n, 10)+")")
	case "reject":
		n := app.Db.MustUpdate("{{reports}}", db.H{"status": 2, "resolved_by": c.User.Id, "resolved_at": now}, db.H{"reported_id": uid, "status": 0})
		logAction(c.Context, uid, "reports-reject", "отклонено жалоб: "+strconv.FormatInt(n, 10))
	case "reopen":
		app.Db.MustUpdate("{{reports}}", db.H{"status": 0, "resolved_by": nil, "resolved_at": nil}, db.H{"id": rep.Int64("id")})
		logAction(c.Context, uid, "report-reopen", "жалоба #"+rep.Str("id"))
	default:
		res["status"] = 500
		res["errors"] = web.M{"modal_message": "Неизвестное действие"}
		return c.Json(res)
	}
	res["status"] = 200
	return c.Json(res)
}
