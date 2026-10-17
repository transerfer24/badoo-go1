package control

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
PermissionsController — админы и разделы (как в StockGo1):

  - «Админы»: кто имеет доступ к админке; добавить админа = найти пользователя и включить ему раздел;
  - права админа: раздел вкл/выкл (control_rules) с записью в control_rules_history; суперадмина не меняют;
  - «Разделы»: справочник control_actions (адрес, название, группа меню, порядок) и групп control_categories.
    Новый раздел сразу открывается всем суперадминам.
*/
type PermissionsController struct{ web.Controller }

func (c *PermissionsController) Behaviors() []web.Behavior { return access("permissions", "index") }

func (c *PermissionsController) Init() { c.Layout = "control" }

func (c *PermissionsController) ActionIndex() error {
	return c.Render("permissions", web.M{"section": "permissions", "uid": c.GetInt64("uid")})
}

// Админы: у кого есть хоть одно право
func (c *PermissionsController) ActionGetUsers() error {
	res := web.M{}
	q := app.Db.Query().
		Select("u.id AS uid", "u.name", "u.email", "u.banned", "u.last_seen", "COUNT(r.id) AS rules",
			"(SELECT COUNT(*) FROM {{control_superadmins}} s WHERE s.uid = u.id) AS is_super").
		From("{{control_rules}} r").
		InnerJoin("{{users}} u", db.Raw("u.id = r.uid")).
		Where(db.H{"r.status": 1})
	if s := strings.TrimSpace(c.Post("search")); s != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
		q.AndWhere(db.Raw("(u.email LIKE :s OR u.name LIKE :s OR u.id = :sid)", db.P{":s": "%" + s + "%", ":sid": id}))
	}
	q.GroupBy("u.id", "u.name", "u.email", "u.banned", "u.last_seen").OrderBy("u.name")

	p, err := paginate(q, max(1, c.PostInt("page", 1)), 50)
	if err != nil {
		app.Log.Error("control: список админов", "err", err)
		res["status"] = 200
		res["items"] = db.Rows{}
		return c.Json(res)
	}
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Права пользователя: все разделы и включён ли каждый
func (c *PermissionsController) ActionGetUserPermissions() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	user := app.Db.CreateCommand("SELECT id AS uid, name, email, banned FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustRow()
	if user == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Пользователь не найден"}
		return c.Json(res)
	}
	res["status"] = 200
	res["user"] = user
	res["is_super"] = IsSuper(uid)
	res["items"] = app.Db.CreateCommand(`
		SELECT a.id, a.action, a.name, a.locked, c.name AS cat_name,
		       EXISTS (SELECT 1 FROM {{control_rules}} r WHERE r.action_id = a.id AND r.uid = :uid AND r.status = 1) AS is_active
		FROM {{control_actions}} a
		LEFT JOIN {{control_categories}} c ON c.id = a.cid
		WHERE a.status = 1
		ORDER BY a.cid ASC, a.priority DESC, a.id ASC
	`).BindValue(":uid", uid).MustAll()
	return c.Json(res)
}

// Включить / выключить раздел админу
func (c *PermissionsController) ActionChangeStatus() error {
	res := web.M{}
	errs := web.M{}

	uid := c.PostInt64("uid")
	actionId := c.PostInt64("id")
	status := c.PostInt("status")

	item := app.Db.CreateCommand("SELECT id, action FROM {{control_actions}} WHERE id = :id AND status = 1 AND locked = 0").BindValue(":id", actionId).MustRow()
	if item == nil {
		errs["modal_message"] = "Раздел не найден или закрыт для изменения"
	}
	if app.Db.CreateCommand("SELECT id FROM {{users}} WHERE id = :uid AND banned = 0").BindValue(":uid", uid).MustInt() == 0 {
		errs["modal_message"] = "Пользователь не найден или заблокирован"
	}
	if IsSuper(uid) {
		errs["modal_message"] = "Права суперадмина не меняются"
	}
	if status != 0 && status != 1 {
		errs["modal_message"] = "Неверный статус"
	}
	// себе «Права» не выключить — иначе потеряешь доступ к этой странице
	if uid == c.User.Id && status == 0 && item != nil && item.Str("action") == "permissions" {
		errs["modal_message"] = "Нельзя снять у себя доступ к правам"
	}

	if len(errs) > 0 {
		res["status"] = 500
		res["message"] = errs["modal_message"]
		return c.Json(res)
	}
	err := app.Db.Transaction(func(tx *db.Tx) error {
		tx.MustDelete("{{control_rules}}", db.H{"uid": uid, "action_id": actionId})
		if status == 1 {
			tx.MustInsert("{{control_rules}}", db.H{"uid": uid, "action_id": actionId, "level": 1, "status": 1})
		}
		tx.MustInsert("{{control_rules_history}}", db.H{
			"user_id": uid, "parent_uid": c.User.Id, "action_id": actionId, "date": time.Now().UTC(), "type": status,
		})
		return nil
	})
	if err != nil {
		app.Log.Error("control: смена права", "err", err)
		res["status"] = 500
		res["message"] = "Сервер временно недоступен"
		return c.Json(res)
	}
	logAction(c.Context, uid, "permission", item.Str("action")+" → "+strconv.Itoa(status))
	res["status"] = 200
	return c.Json(res)
}

// История изменений прав пользователя
func (c *PermissionsController) ActionGetHistory() error {
	res := web.M{}
	res["status"] = 200
	res["items"] = prepare(app.Db.CreateCommand(`
		SELECT h.date, h.type, a.name AS action_name, p.name AS parent_name, h.parent_uid
		FROM {{control_rules_history}} h
		LEFT JOIN {{control_actions}} a ON a.id = h.action_id
		LEFT JOIN {{users}} p ON p.id = h.parent_uid
		WHERE h.user_id = :uid
		ORDER BY h.id DESC LIMIT 50
	`).BindValue(":uid", c.PostInt64("uid")).MustAll())
	return c.Json(res)
}

// Поиск пользователя для «Добавить админа»
func (c *PermissionsController) ActionSearchUser() error {
	return c.Json(web.M{"items": searchUsers(c.Post("q"))})
}

// ── Разделы и группы меню ──────────────────────────────────────────────────

func (c *PermissionsController) ActionGetSections() error {
	res := web.M{}
	res["status"] = 200
	res["items"] = app.Db.CreateCommand(`
		SELECT a.id, a.action, a.name, a.cid, a.priority, a.status, a.locked, c.name AS cat_name,
		       (SELECT COUNT(*) FROM {{control_rules}} r WHERE r.action_id = a.id AND r.status = 1) AS admins
		FROM {{control_actions}} a
		LEFT JOIN {{control_categories}} c ON c.id = a.cid
		ORDER BY a.cid ASC, a.priority DESC, a.id ASC
	`).MustAll()
	res["categories"] = app.Db.CreateCommand("SELECT id, name FROM {{control_categories}} ORDER BY name").MustAll()
	return c.Json(res)
}

var sectionAction = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)

// Добавить / изменить раздел. Новый — сразу всем суперадминам.
func (c *PermissionsController) ActionSaveSection() error {
	res := web.M{}
	errs := web.M{}

	id := c.PostInt64("id")
	action := strings.ToLower(strings.TrimSpace(c.Post("action")))
	name := strings.TrimSpace(c.Post("name"))
	cid := c.PostInt64("cid")
	priority := c.PostInt("priority")
	status := c.PostInt("status", 1)
	locked := c.PostInt("locked")
	if status != 1 {
		status = 0
	}
	if locked != 1 {
		locked = 0
	}

	if !sectionAction.MatchString(action) {
		errs["action"] = "Латиница, цифры и дефис, до 30 символов (адрес /control/<раздел>)"
	}
	if name == "" || utf8.RuneCountInString(name) > 30 {
		errs["name"] = "Название — от 1 до 30 символов"
	}
	if cid > 0 && app.Db.CreateCommand("SELECT id FROM {{control_categories}} WHERE id = :id").BindValue(":id", cid).MustInt() == 0 {
		errs["cid"] = "Группа не найдена"
	}
	if (action == "permissions" || action == "index") && status == 0 {
		errs["modal_message"] = "Дашборд и права выключить нельзя"
	}
	if app.Db.CreateCommand("SELECT id FROM {{control_actions}} WHERE action = :a AND id <> :id").BindValue(":a", action).BindValue(":id", id).MustInt() > 0 {
		errs["action"] = "Раздел с таким адресом уже есть"
	}

	if len(errs) > 0 {
		res["status"] = 500
		res["errors"] = errs
		return c.Json(res)
	}
	h := db.H{"action": action, "name": name, "cid": cid, "priority": priority, "status": status, "locked": locked}
	err := app.Db.Transaction(func(tx *db.Tx) error {
		if id > 0 {
			tx.MustUpdate("{{control_actions}}", h, db.H{"id": id})
			return nil
		}
		id = tx.MustInsert("{{control_actions}}", h)
		tx.CreateCommand("INSERT INTO {{control_rules}} (uid, action_id, level, status) SELECT uid, :id, 1, 1 FROM {{control_superadmins}}", db.P{":id": id}).MustExecute()
		return nil
	})
	if err != nil {
		res["status"] = 500
		res["errors"] = web.M{"modal_message": "Ошибка сохранения"}
		return c.Json(res)
	}
	logAction(c.Context, 0, "section", action+" «"+name+"»")
	res["status"] = 200
	return c.Json(res)
}

// Группа меню (выпадающий пункт)
func (c *PermissionsController) ActionSaveCategory() error {
	res := web.M{}
	id := c.PostInt64("id")
	name := strings.TrimSpace(c.Post("name"))
	if name == "" || utf8.RuneCountInString(name) > 50 {
		res["status"] = 500
		res["errors"] = web.M{"name": "Название — от 1 до 50 символов"}
		return c.Json(res)
	}
	if id > 0 {
		app.Db.MustUpdate("{{control_categories}}", db.H{"name": name}, db.H{"id": id})
	} else {
		id = app.Db.MustInsert("{{control_categories}}", db.H{"name": name})
	}
	res["status"] = 200
	res["id"] = id
	return c.Json(res)
}
