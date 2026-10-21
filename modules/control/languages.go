package control

import (
	"regexp"
	"strings"

	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
LanguagesController — справочник языков анкеты (таблица languages, выбор людей — users.languages «код:уровень»).

Код — ISO 639-1 (или 639-3, если двухбуквенного нет), после создания не меняется: на него ссылаются анкеты.
Удалить можно только язык, который никто не выбрал; остальные — скрыть (active = 0): у кого выбран, тот его
сохранит, новым в списке выбора его не будет. После любой правки — users.LangsChanged() (кеш справочника).
*/
type LanguagesController struct{ web.Controller }

func (c *LanguagesController) Behaviors() []web.Behavior { return access("languages", "index") }

func (c *LanguagesController) Init() { c.Layout = "control" }

func (c *LanguagesController) ActionIndex() error {
	return c.Render("languages", web.M{"section": "languages"})
}

// usersOf — сколько анкет выбрали язык l (users.languages — «uk:fluent,en:speak»)
const usersOf = `(SELECT COUNT(*) FROM {{users}} u WHERE CONCAT(',', u.languages) LIKE CONCAT('%,', l.code, ':%')) AS users`

func (c *LanguagesController) ActionGetLanguages() error {
	res := web.M{}
	q := app.Db.Query().Select("l.id", "l.code", "l.name", "l.native", "l.pos", "l.active", usersOf).From("{{languages}} l")
	switch c.Post("active") {
	case "1":
		q.AndWhere(db.H{"l.active": 1})
	case "0":
		q.AndWhere(db.H{"l.active": 0})
	}
	if s := strings.TrimSpace(c.Post("search")); s != "" {
		q.AndWhere(db.Raw("(l.name LIKE :s OR l.native LIKE :s OR l.code = :code)", db.P{":s": "%" + s + "%", ":code": strings.ToLower(s)}))
	}
	if c.Post("sort") == "users" {
		q.OrderBy("users DESC", "l.pos", "l.name")
	} else {
		q.OrderBy("l.pos", "l.name")
	}
	p, err := paginate(q, max(1, c.PostInt("page", 1)), 50)
	if err != nil {
		app.Log.Error("control: языки", "err", err)
		res["status"] = 200
		res["items"] = db.Rows{}
		return c.Json(res)
	}
	res["status"] = 200
	res["items"] = p.Items
	res["count"] = p.Total
	return c.Json(res)
}

var langCode = regexp.MustCompile(`^[a-z]{2,3}$`)

// Создать (id = 0) или изменить язык.
func (c *LanguagesController) ActionSave() error {
	res := web.M{}
	id := c.PostInt64("id")
	name, native := strings.TrimSpace(c.Post("name")), strings.TrimSpace(c.Post("native"))
	code := strings.ToLower(strings.TrimSpace(c.Post("code")))
	fail := func(field, msg string) error {
		res["status"] = 500
		res["errors"] = web.M{field: msg}
		return c.Json(res)
	}
	if l := len([]rune(name)); l < 2 || l > 64 {
		return fail("name", "Название - от 2 до 64 символов")
	}
	if len([]rune(native)) > 64 {
		return fail("native", "Самоназвание - до 64 символов")
	}
	set := db.H{"name": name, "native": native, "pos": c.PostInt("pos", 1000), "active": c.PostInt("active", 1) == 1}
	if id == 0 {
		if !langCode.MatchString(code) {
			return fail("code", "Код - 2-3 латинские буквы (ISO 639)")
		}
		if app.Db.CreateCommand("SELECT COUNT(*) FROM {{languages}} WHERE code = :c").BindValue(":c", code).MustInt() > 0 {
			return fail("code", "Такой код уже есть")
		}
		set["code"] = code
		app.Db.MustInsert("{{languages}}", set)
		logAction(c.Context, 0, "language-add", code+" "+name)
	} else {
		old := app.Db.CreateCommand("SELECT code FROM {{languages}} WHERE id = :id").BindValue(":id", id).MustString()
		if old == "" {
			return fail("modal_message", "Язык не найден")
		}
		app.Db.MustUpdate("{{languages}}", set, db.H{"id": id})
		logAction(c.Context, 0, "language-edit", old+" "+name)
	}
	users.LangsChanged()
	res["status"] = 200
	return c.Json(res)
}

// Скрыть / показать в выборе.
func (c *LanguagesController) ActionToggle() error {
	res := web.M{}
	l := app.Db.CreateCommand("SELECT id, code, active FROM {{languages}} WHERE id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if l == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Язык не найден"}
		return c.Json(res)
	}
	on := !l.Bool("active")
	app.Db.MustUpdate("{{languages}}", db.H{"active": on}, db.H{"id": l.Int64("id")})
	users.LangsChanged()
	if on {
		logAction(c.Context, 0, "language-show", l.Str("code"))
	} else {
		logAction(c.Context, 0, "language-hide", l.Str("code"))
	}
	res["status"] = 200
	return c.Json(res)
}

// Удалить — только если язык никто не выбрал.
func (c *LanguagesController) ActionDelete() error {
	res := web.M{}
	l := app.Db.CreateCommand("SELECT l.id, l.code, "+usersOf+" FROM {{languages}} l WHERE l.id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if l == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Язык не найден"}
		return c.Json(res)
	}
	if l.Int("users") > 0 {
		res["status"] = 500
		res["errors"] = web.M{"modal_message": "Этот язык выбран в анкетах - его можно только скрыть"}
		return c.Json(res)
	}
	app.Db.MustDelete("{{languages}}", db.H{"id": l.Int64("id")})
	users.LangsChanged()
	logAction(c.Context, 0, "language-delete", l.Str("code"))
	res["status"] = 200
	return c.Json(res)
}
