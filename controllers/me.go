package controllers

import (
	"time"

	"badoo/components/photos"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
MeController — моя анкета, фото, настройки, Premium, верификация (перенос handlers_me.go).

	GET/POST /me                      анкета
	POST /me/photos                   загрузить фото
	POST /me/photos/<id>/delete       удалить
	POST /me/photos/<id>/main         сделать главным
	GET/POST /settings                фильтры и приватность
	POST /settings/delete             удалить аккаунт
	GET  /premium, POST /premium/activate|cancel
	GET/POST /verify                  селфи на проверку
*/
type MeController struct{ AppController }

func (c *MeController) Init() {
	// фото при онбординге и удаление аккаунта — без заполненной анкеты (было a.auth(false, …))
	c.NoOnboard = []string{"photo-upload", "photo-delete", "photo-main", "delete-account"}
}

type photoRow struct {
	Id  int64
	Url string
}

func (c *MeController) myPhotos() []photoRow {
	out := []photoRow{}
	for _, r := range c.Db().CreateCommand("SELECT id, file FROM {{photos}} WHERE user_id = :uid ORDER BY pos, id").BindValue(":uid", c.User.Id).MustAll() {
		out = append(out, photoRow{Id: r.Int64("id"), Url: photos.Url(r.Str("file"))})
	}
	return out
}

func (c *MeController) mePage(errMsg string) error {
	me := users.Current(c.Context)
	mine := map[string]bool{}
	for _, i := range me.InterestList() {
		mine[i] = true
	}
	c.View.Params = web.M{"active": "me"}

	return c.Render("index", web.M{
		"me": me, "error": errMsg, "photos": c.myPhotos(), "max_photos": photos.MaxPhotos,
		"cities": users.Cities, "interests": users.InterestOptions, "mine": mine, "max": users.MaxBirthdate(),
	})
}

func (c *MeController) ActionIndex() error {
	if !c.Request.IsPost {
		return c.mePage("")
	}
	set, errMsg := users.ParseProfileForm(c.Context, false)
	if errMsg != "" {
		return c.mePage(errMsg)
	}
	if _, err := app.Db.Update("{{users}}", set, db.H{"id": c.User.Id}); err != nil {
		return c.mePage("Не удалось сохранить.")
	}
	c.Session.SetFlash("ok", "Сохранено.")
	return c.Redirect("/me")
}

func (c *MeController) ActionPhotoUpload() error {
	if len(c.myPhotos()) >= photos.MaxPhotos {
		c.Session.SetFlash("ok", "Максимум 6 фото. Удалите одно, чтобы добавить новое.")
		return c.Redirect("/me")
	}
	name, err := photos.Store(c.Context, "photo", photos.KindPhoto)
	if err != nil {
		c.Session.SetFlash("ok", err.Error())
		return c.Redirect("/me")
	}
	pos := c.Db().CreateCommand("SELECT COALESCE(MAX(pos), 0) + 1 FROM {{photos}} WHERE user_id = :uid").BindValue(":uid", c.User.Id).MustInt()
	app.Db.MustInsert("{{photos}}", db.H{"user_id": c.User.Id, "file": name, "pos": pos})
	c.Session.SetFlash("ok", "Фото добавлено.")
	return c.Redirect("/me")
}

func (c *MeController) ActionPhotoDelete() error {
	id := c.GetInt64("id")
	row := c.Db().CreateCommand("SELECT file FROM {{photos}} WHERE id = :id AND user_id = :uid").
		BindValue(":id", id).BindValue(":uid", c.User.Id).MustRow()
	if row != nil {
		app.Db.MustDelete("{{photos}}", db.H{"id": id, "user_id": c.User.Id})
		photos.Delete(row.Str("file"))
	}
	c.Session.SetFlash("ok", "Фото удалено.")
	return c.Redirect("/me")
}

func (c *MeController) ActionPhotoMain() error {
	id := c.GetInt64("id")
	if n := app.Db.MustUpdate("{{photos}}", db.H{"pos": 0}, db.H{"id": id, "user_id": c.User.Id}); n == 1 {
		app.Db.CreateCommand("UPDATE {{photos}} SET pos = pos + 1 WHERE user_id = :uid AND id <> :id", db.P{":uid": c.User.Id, ":id": id}).MustExecute()
	}
	c.Session.SetFlash("ok", "Главное фото обновлено.")
	return c.Redirect("/me")
}

// ---------- Настройки ----------

func (c *MeController) ActionSettings() error {
	me := users.Current(c.Context)
	c.View.Params = web.M{"active": "me"}
	if !c.Request.IsPost {
		return c.Render("settings", web.M{"me": me})
	}

	amin, amax := clamp(c.PostInt("age_min"), 18, 99), clamp(c.PostInt("age_max"), 18, 99)
	if amax < amin {
		amin, amax = amax, amin
	}
	km := clamp(c.PostInt("max_km"), 1, 300)
	seeking := c.Post("seeking")
	if seeking != "m" && seeking != "f" && seeking != "any" {
		seeking = me.Seeking
	}
	wantInvisible := c.Post("invisible") == "1"
	app.Db.MustUpdate("{{users}}", db.H{
		"age_min": amin, "age_max": amax, "max_km": km, "seeking": seeking,
		"verified_only": c.Post("verified_only") == "1", "invisible": wantInvisible && me.IsPremium(),
	}, db.H{"id": c.User.Id})

	msg := "Настройки сохранены."
	if wantInvisible && !me.IsPremium() {
		msg = "Настройки сохранены. Невидимка доступна с Premium."
	}
	c.Session.SetFlash("ok", msg)
	return c.Redirect("/settings")
}

func (c *MeController) ActionDeleteAccount() error {
	if c.Post("confirm") != "УДАЛИТЬ" {
		c.Session.SetFlash("ok", "Чтобы удалить аккаунт, введите слово УДАЛИТЬ.")
		return c.Redirect("/settings")
	}
	uid := c.User.Id
	for _, p := range c.Db().CreateCommand("SELECT file FROM {{photos}} WHERE user_id = :uid").BindValue(":uid", uid).MustColumn() {
		photos.Delete(p.(string))
	}
	if vf := c.Db().CreateCommand("SELECT verify_file FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustString(); vf != "" {
		photos.Delete(vf)
	}
	// В SQLite-версии связанное удалялось каскадом (ON DELETE CASCADE). Здесь внешних ключей нет — чистим сами.
	err := app.Db.Transaction(func(tx *db.Tx) error {
		for _, t := range []struct{ table, col string }{
			{"photos", "user_id"}, {"votes", "voter_id"}, {"votes", "target_id"}, {"visits", "visitor_id"}, {"visits", "visited_id"},
			{"blocks", "blocker_id"}, {"blocks", "blocked_id"}, {"reports", "reporter_id"}, {"reports", "reported_id"},
			{"sessions", "user_id"}, {"push_tokens", "user_id"}, {"meetup_going", "user_id"},
		} {
			tx.MustDelete("{{"+t.table+"}}", db.H{t.col: uid})
		}
		tx.CreateCommand("DELETE FROM {{messages}} WHERE match_id IN (SELECT id FROM {{matches}} WHERE user_a = :uid OR user_b = :uid)", db.P{":uid": uid}).MustExecute()
		tx.MustDelete("{{matches}}", db.Or(db.H{"user_a": uid}, db.H{"user_b": uid}))
		tx.MustUpdate("{{meetups}}", db.H{"host_id": nil}, db.H{"host_id": uid})
		tx.MustDelete("{{users}}", db.H{"id": uid})
		return nil
	})
	if err != nil {
		return err
	}
	_ = c.User.Logout()
	c.Session.SetFlash("ok", "Аккаунт и все данные удалены.")
	return c.Redirect("/")
}

// ---------- Premium ----------

type plan struct {
	Id     string
	Title  string
	Days   int
	Price  string
	Per    string
	Accent bool
}

var plans = []plan{
	{"week", "1 неделя", 7, "4,99 €", "", false},
	{"month", "1 месяц", 30, "12,99 €", "", true},
	{"quarter", "3 месяца", 90, "29,99 €", "10 € / мес", false},
}

func (c *MeController) ActionPremium() error {
	c.View.Params = web.M{"active": "me"}
	return c.Render("premium", web.M{"me": users.Current(c.Context), "plans": plans})
}

// В dev-режиме Premium активируется без оплаты. В проде здесь будет Stripe Checkout.
func (c *MeController) ActionPremiumActivate() error {
	me := users.Current(c.Context)
	if !devMode() {
		c.Session.SetFlash("ok", "Оплата ещё не подключена.")
		return c.Redirect("/premium")
	}
	days := 30
	for _, p := range plans {
		if p.Id == c.Post("plan") {
			days = p.Days
		}
	}
	start := time.Now()
	if me.IsPremium() {
		start = me.PremiumUntil
	}
	app.Db.MustUpdate("{{users}}", db.H{"premium_until": start.AddDate(0, 0, days).UTC()}, db.H{"id": c.User.Id})
	c.Session.SetFlash("ok", "Premium активирован (тестовый режим, без оплаты).")
	return c.Redirect("/premium")
}

func (c *MeController) ActionPremiumCancel() error {
	app.Db.MustUpdate("{{users}}", db.H{"premium_until": nil, "invisible": false}, db.H{"id": c.User.Id})
	c.Session.SetFlash("ok", "Подписка отменена. Больше никаких списаний.")
	return c.Redirect("/premium")
}

// ---------- Верификация ----------

var verifyPoses = []string{"Покажите ладонь у щеки", "Покажите знак «ок»", "Поднимите большой палец", "Прикройте один глаз ладонью"}

func (c *MeController) ActionVerify() error {
	me := users.Current(c.Context)
	c.View.Params = web.M{"active": "me"}
	if !c.Request.IsPost {
		return c.Render("verify", web.M{"me": me, "pose": verifyPoses[int(me.Id)%len(verifyPoses)]})
	}

	// Селфи хранится в закрытой папке и не раздаётся наружу.
	name, err := photos.Store(c.Context, "selfie", photos.KindPrivate)
	if err != nil {
		c.Session.SetFlash("ok", err.Error())
		return c.Redirect("/verify")
	}
	status := 1
	if devMode() {
		status = 2 // в dev-режиме одобряем сразу; в проде — очередь модерации
	}
	app.Db.MustUpdate("{{users}}", db.H{"verified": status, "verify_file": name}, db.H{"id": c.User.Id})
	if status == 2 {
		c.Session.SetFlash("ok", "Готово! Анкета подтверждена — теперь можно писать сообщения.")
	} else {
		c.Session.SetFlash("ok", "Селфи отправлено на проверку. Обычно это занимает до часа.")
	}
	return c.Redirect("/verify")
}

func devMode() bool { v, _ := app.Params["amura.dev"].(bool); return v }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
