package controllers

import (
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

func (c *MeController) mePage(errMsg string) error {
	me := users.Current(c.Context)
	mine := map[string]bool{}
	for _, i := range me.InterestList() {
		mine[i] = true
	}
	c.View.Params = web.M{"active": "me"}

	return c.Render("index", web.M{
		"me": me, "error": errMsg, "photos": photos.Mine(c.User.Id), "max_photos": photos.MaxPhotos,
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
	if len(photos.Mine(c.User.Id)) >= photos.MaxPhotos {
		c.Session.SetFlash("ok", "Максимум 6 фото. Удалите одно, чтобы добавить новое.")
		return c.Redirect("/me")
	}
	name, err := photos.Store(c.Context, "photo", photos.KindPhoto)
	if err != nil {
		c.Session.SetFlash("ok", err.Error())
		return c.Redirect("/me")
	}
	photos.Add(c.User.Id, name)
	c.Session.SetFlash("ok", "Фото добавлено.")
	return c.Redirect("/me")
}

func (c *MeController) ActionPhotoDelete() error {
	photos.Remove(c.User.Id, c.GetInt64("id"))
	c.Session.SetFlash("ok", "Фото удалено.")
	return c.Redirect("/me")
}

func (c *MeController) ActionPhotoMain() error {
	photos.SetMain(c.User.Id, c.GetInt64("id"))
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

	wantInvisible := c.Post("invisible") == "1"
	users.SaveSettings(me, users.Settings{
		AgeMin: c.PostInt("age_min"), AgeMax: c.PostInt("age_max"), MaxKm: c.PostInt("max_km"), Seeking: c.Post("seeking"),
		VerifiedOnly: c.Post("verified_only") == "1", Invisible: wantInvisible,
	})

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
	if err := users.Delete(c.User.Id); err != nil {
		return err
	}
	_ = c.User.Logout()
	c.Session.SetFlash("ok", "Аккаунт и все данные удалены.")
	return c.Redirect("/")
}

// ---------- Premium ----------

func (c *MeController) ActionPremium() error {
	c.View.Params = web.M{"active": "me"}
	// dev — тестовый режим (AMURA_DEV): оплата не списывается, страница говорит это прямо
	return c.Render("premium", web.M{"me": users.Current(c.Context), "plans": users.Plans, "dev": users.DevMode()})
}

// В dev-режиме Premium активируется без оплаты. В проде здесь будет Stripe Checkout.
func (c *MeController) ActionPremiumActivate() error {
	me := users.Current(c.Context)
	if !users.DevMode() {
		c.Session.SetFlash("ok", "Оплата ещё не подключена.")
		return c.Redirect("/premium")
	}
	if !users.ActivatePremium(me, c.Post("plan", "month")) {
		c.Session.SetFlash("ok", "Выберите тариф.")
		return c.Redirect("/premium")
	}
	c.Session.SetFlash("ok", "Premium активирован (тестовый режим, без оплаты).")
	return c.Redirect("/premium")
}

func (c *MeController) ActionPremiumCancel() error {
	users.CancelPremium(c.User.Id)
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
	if users.SetVerified(c.User.Id, name) == 2 {
		c.Session.SetFlash("ok", "Готово! Анкета подтверждена — теперь можно писать сообщения.")
	} else {
		c.Session.SetFlash("ok", "Селфи отправлено на проверку. Обычно это занимает до часа.")
	}
	return c.Redirect("/verify")
}
