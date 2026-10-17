package control

import (
	"strconv"
	"strings"
	"time"

	"badoo/components/meetups"
	"badoo/components/photos"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

// ── Верификация ────────────────────────────────────────────────────────────

/*
VerificationController — очередь селфи: users.verified = 1 (на проверке). Рядом — фото анкеты, чтобы сравнить
лицо. Подтвердить → 2 (галочка в анкете), отклонить → 0 и селфи удаляется (человек может прислать новое).
В dev-режиме (AMURA_DEV=1) селфи подтверждается сразу и очередь пуста.
*/
type VerificationController struct{ web.Controller }

func (c *VerificationController) Behaviors() []web.Behavior {
	return access("verification", "index")
}

func (c *VerificationController) Init() { c.Layout = "control" }

func (c *VerificationController) ActionIndex() error {
	return c.Render("verification", web.M{"section": "verification", "dev": users.DevMode()})
}

// status = 1 — очередь, 2 — подтверждённые (с селфи, для перепроверки)
func (c *VerificationController) ActionGetQueue() error {
	res := web.M{}
	status := 1
	if c.Post("status") == "2" {
		status = 2
	}
	p := mustPaginate(app.Db.Query().
		Select("u.id", "u.name", "u.email", "u.gender", "u.birthdate", "u.city", "u.verify_file", "u.created_at", "u.last_seen",
			"(SELECT GROUP_CONCAT(p.file ORDER BY p.pos, p.id) FROM {{photos}} p WHERE p.user_id = u.id) AS photo_list",
			"(SELECT COUNT(*) FROM {{reports}} r WHERE r.reported_id = u.id) AS reports_count").
		From("{{users}} u").
		Where(db.Raw("u.verified = :st AND u.banned = 0 AND u.verify_file <> ''", db.P{":st": status})).
		OrderBy("u.id ASC"), max(1, c.PostInt("page", 1)), 20)
	for _, r := range p.Items {
		list := []string{}
		for _, f := range users.SplitList(r.Str("photo_list")) {
			list = append(list, photos.Url(f))
		}
		r["photos"] = list
		r["age"] = users.AgeOf(r.Str("birthdate"))
		delete(r, "photo_list")
	}
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Решение: verified = 2 подтвердить, 0 отклонить
func (c *VerificationController) ActionDecide() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	v := c.PostInt("verified")
	if v != 0 && v != 2 {
		res["status"] = 500
		res["errors"] = web.M{"modal_message": "Неверное решение"}
		return c.Json(res)
	}
	users.Verify(uid, v == 2)
	if v == 2 {
		logAction(c.Context, uid, "verify", "")
	} else {
		logAction(c.Context, uid, "unverify", strings.TrimSpace(c.Post("reason")))
	}
	res["status"] = 200
	return c.Json(res)
}

// ── Фото ───────────────────────────────────────────────────────────────────

// PhotosController — лента всех фото анкет (свежие сверху) для просмотра и удаления неподходящих.
type PhotosController struct{ web.Controller }

func (c *PhotosController) Behaviors() []web.Behavior { return access("photos", "index") }

func (c *PhotosController) Init() { c.Layout = "control" }

func (c *PhotosController) ActionIndex() error {
	return c.Render("photos", web.M{"section": "photos"})
}

func (c *PhotosController) ActionGetPhotos() error {
	res := web.M{}
	q := app.Db.Query().
		Select("p.id", "p.file", "p.pos", "u.id AS uid", "u.name", "u.gender", "u.banned", "u.verified").
		From("{{photos}} p").
		InnerJoin("{{users}} u", db.Raw("u.id = p.user_id"))
	if uid := c.PostInt64("uid"); uid > 0 {
		q.AndWhere(db.H{"p.user_id": uid})
	}
	if g := c.Post("gender"); g == "m" || g == "f" || g == "x" {
		q.AndWhere(db.H{"u.gender": g})
	}
	if c.Post("main") == "1" {
		q.AndWhere(db.Raw("p.id = (SELECT p2.id FROM {{photos}} p2 WHERE p2.user_id = p.user_id ORDER BY p2.pos, p2.id LIMIT 1)"))
	}
	q.OrderBy("p.id DESC")
	p := mustPaginate(q, max(1, c.PostInt("page", 1)), 60)
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

func (c *PhotosController) ActionDelete() error {
	res := web.M{}
	ph := app.Db.CreateCommand("SELECT id, user_id, file FROM {{photos}} WHERE id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if ph == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Фото не найдено"}
		return c.Json(res)
	}
	photos.Delete(ph.Str("file"))
	app.Db.MustDelete("{{photos}}", db.H{"id": ph.Int64("id")})
	logAction(c.Context, ph.Int64("user_id"), "photo-delete", "фото #"+ph.Str("id"))
	res["status"] = 200
	return c.Json(res)
}

// ── Встречи ────────────────────────────────────────────────────────────────

// MeetupsController — события «Встреч»: предстоящие и прошедшие, кто идёт и кто отметился, удаление.
type MeetupsController struct{ web.Controller }

func (c *MeetupsController) Behaviors() []web.Behavior { return access("meetups", "index", "view") }

func (c *MeetupsController) Init() { c.Layout = "control" }

func (c *MeetupsController) ActionIndex() error {
	return c.Render("meetups", web.M{"section": "meetups", "kinds": meetups.Kinds, "cities": users.Cities})
}

func (c *MeetupsController) ActionView() error {
	id := c.GetInt64("id")
	if app.Db.CreateCommand("SELECT id FROM {{meetups}} WHERE id = :id").BindValue(":id", id).MustInt() == 0 {
		return c.Redirect("/control/meetups")
	}
	return c.Render("view", web.M{"section": "meetups", "id": id})
}

func (c *MeetupsController) ActionGetMeetups() error {
	res := web.M{}
	now := time.Now().UTC()
	q := app.Db.Query().
		Select("m.id", "m.title", "m.kind", "m.place", "m.city", "m.starts_at", "m.ends_at", "m.capacity", "m.price", "m.demo",
			"m.host_id", "h.name AS host_name", "h.banned AS host_banned", "m.created_at",
			"(SELECT COUNT(*) FROM {{meetup_going}} g WHERE g.meetup_id = m.id) AS going",
			"(SELECT COUNT(*) FROM {{meetup_going}} g WHERE g.meetup_id = m.id AND g.checked_in_at IS NOT NULL) AS checked_in").
		From("{{meetups}} m").
		LeftJoin("{{users}} h", db.Raw("h.id = m.host_id"))
	switch c.Post("when") {
	case "past":
		q.AndWhere(db.Raw("m.ends_at <= :now", db.P{":now": now})).OrderBy("m.starts_at DESC")
	case "all":
		q.OrderBy("m.starts_at DESC")
	default:
		q.AndWhere(db.Raw("m.ends_at > :now", db.P{":now": now})).OrderBy("m.starts_at ASC")
	}
	switch c.Post("demo") {
	case "1":
		q.AndWhere(db.Raw("m.demo = 1"))
	case "0":
		q.AndWhere(db.Raw("m.demo = 0"))
	}
	if city := strings.TrimSpace(c.Post("city")); city != "" {
		q.AndWhere(db.H{"m.city": city})
	}
	if kind := c.Post("kind"); kind != "" {
		q.AndWhere(db.H{"m.kind": kind})
	}
	if s := strings.TrimSpace(c.Post("search")); s != "" {
		q.AndWhere(db.Raw("(m.title LIKE :s OR m.place LIKE :s OR h.name LIKE :s)", db.P{":s": "%" + s + "%"}))
	}
	p := mustPaginate(q, max(1, c.PostInt("page", 1)), 50)
	for _, r := range p.Items {
		if k, ok := meetups.KindByKey(r.Str("kind")); ok {
			r["kind_label"] = k.Label
		}
	}
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

func (c *MeetupsController) ActionGetMeetup() error {
	res := web.M{}
	id := c.PostInt64("id")
	item := app.Db.CreateCommand(`SELECT m.*, h.name AS host_name, h.banned AS host_banned
		FROM {{meetups}} m LEFT JOIN {{users}} h ON h.id = m.host_id WHERE m.id = :id`).BindValue(":id", id).MustRow()
	if item == nil {
		res["status"] = 404
		return c.Json(res)
	}
	if k, ok := meetups.KindByKey(item.Str("kind")); ok {
		item["kind_label"] = k.Label
	}
	res["status"] = 200
	res["item"] = prepareRow(item)
	res["going"] = prepare(app.Db.CreateCommand(`
		SELECT u.id, u.name, u.gender, u.birthdate, u.banned, g.created_at, g.checked_in_at,
		       (SELECT p.file FROM {{photos}} p WHERE p.user_id = u.id ORDER BY p.pos, p.id LIMIT 1) AS photo
		FROM {{meetup_going}} g INNER JOIN {{users}} u ON u.id = g.user_id
		WHERE g.meetup_id = :id ORDER BY g.checked_in_at IS NULL, g.created_at`).BindValue(":id", id).MustAll())
	return c.Json(res)
}

func (c *MeetupsController) ActionDelete() error {
	res := web.M{}
	m := app.Db.CreateCommand("SELECT id, title, host_id FROM {{meetups}} WHERE id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if m == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Событие не найдено"}
		return c.Json(res)
	}
	app.Db.Transaction(func(tx *db.Tx) error {
		tx.MustDelete("{{meetup_going}}", db.H{"meetup_id": m.Int64("id")})
		tx.MustDelete("{{meetups}}", db.H{"id": m.Int64("id")})
		return nil
	})
	logAction(c.Context, m.Int64("host_id"), "meetup-delete", "#"+m.Str("id")+" "+m.Str("title"))
	res["status"] = 200
	return c.Json(res)
}

// ── Журнал действий ────────────────────────────────────────────────────────

// LogController — кто из админов что сделал (control_log): блокировки, верификация, удаление фото и сообщений…
type LogController struct{ web.Controller }

func (c *LogController) Behaviors() []web.Behavior { return access("log", "index") }

func (c *LogController) Init() { c.Layout = "control" }

func (c *LogController) ActionIndex() error {
	return c.Render("log", web.M{"section": "log"})
}

func (c *LogController) ActionGetLog() error {
	res := web.M{}
	q := c.logQuery()
	p := mustPaginate(q, max(1, c.PostInt("page", 1)), 50)
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	res["admins"] = app.Db.CreateCommand(`SELECT DISTINCT l.admin_id AS id, a.name FROM {{control_log}} l
		LEFT JOIN {{users}} a ON a.id = l.admin_id ORDER BY a.name`).MustAll()
	return c.Json(res)
}

// logQuery — выборка журнала с фильтрами из POST (общая для списка и выгрузки CSV)
func (c *LogController) logQuery() *db.Query {
	q := app.Db.Query().
		Select("l.id", "l.action", "l.details", "l.ip", "l.created_at", "l.admin_id", "a.name AS admin_name", "l.user_id", "u.name AS user_name").
		From("{{control_log}} l").
		LeftJoin("{{users}} a", db.Raw("a.id = l.admin_id")).
		LeftJoin("{{users}} u", db.Raw("u.id = l.user_id"))
	if act := c.Post("action"); act != "" {
		q.AndWhere(db.H{"l.action": act})
	}
	if id := c.PostInt64("admin_id"); id > 0 {
		q.AndWhere(db.H{"l.admin_id": id})
	}
	if s := strings.TrimSpace(c.Post("search")); s != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
		q.AndWhere(db.Raw("(l.details LIKE :s OR u.name LIKE :s OR l.user_id = :sid)", db.P{":s": "%" + s + "%", ":sid": id}))
	}
	q.OrderBy("l.id DESC")
	return q
}

// Выгрузка CSV по текущим фильтрам
func (c *LogController) ActionExport() error {
	return csvExport(c.Context, "control-log", c.logQuery(), [][2]string{
		{"id", "#"}, {"created_at", "Когда"}, {"admin_id", "Админ #"}, {"admin_name", "Админ"}, {"action", "Действие"},
		{"user_id", "Пользователь #"}, {"user_name", "Пользователь"}, {"details", "Подробности"}, {"ip", "IP"},
	}, "action", "admin_id", "search")
}
