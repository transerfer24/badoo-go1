package controllers

import (
	"strings"
	"time"

	"badoo/components/photos"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
ApiMeController — моя анкета в приложении (перенос профильной части api_v1.go и push.go):

	GET    /api/v1/me                    анкета (MeDTO)
	PUT    /api/v1/me                    онбординг и редактирование — валидация та же, что в вебе (users.ParseProfileForm)
	DELETE /api/v1/me   {confirm}        удалить аккаунт
	PUT    /api/v1/me/settings           фильтры и приватность
	POST   /api/v1/me/photos  (photo)    загрузить фото
	DELETE /api/v1/me/photos/<id>        удалить
	POST   /api/v1/me/photos/<id>/main   главное
	POST   /api/v1/me/verify  (selfie)   селфи на проверку
	POST   /api/v1/me/push    {token, platform}   токен уведомлений Expo
	DELETE /api/v1/me/push    {token}

Все ответы, кроме delete/push, — обновлённая анкета (writeMe), приложение просто перерисовывает экран.
*/
type ApiMeController struct{ ApiController }

func (c *ApiMeController) Init() {
	// без заполненной анкеты: сама анкета (её и заполняем), фото, удаление, push — как a.auth(false, …)
	c.NoOnboard = []string{"index", "update", "delete", "photo-upload", "photo-delete", "photo-main", "push-register", "push-unregister"}
}

func (c *ApiMeController) ActionIndex() error { return writeMe(c.Context, c.User.Id) }

// PUT /api/v1/me — поля приходят JSON-ом; c.Post читает их так же, как форму, поэтому разбор общий с вебом.
func (c *ApiMeController) ActionUpdate() error {
	set, msg := users.ParseProfileForm(c.Context, false)
	if msg != "" {
		return apiFail(c.Context, 400, "validation", msg)
	}
	set["onboarded"] = true
	if _, err := app.Db.Update("{{users}}", set, db.H{"id": c.User.Id}); err != nil {
		return apiFail(c.Context, 500, "db")
	}
	return writeMe(c.Context, c.User.Id)
}

// PUT /api/v1/me/languages {languages: [{code, level}]} → MeDTO. Отдельно от PUT /me: старые сборки приложения
// языков не знают, и их сохранение анкеты не должно стирать выбор, сделанный на сайте.
func (c *ApiMeController) ActionLanguages() error {
	var in struct {
		Languages []users.UserLang `json:"languages"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	me := users.Current(c.Context)
	if len(in.Languages) > users.MaxLangs {
		return apiFail(c.Context, 400, "validation", "Не больше 5 языков.")
	}
	langs := users.NormalizeLangs(in.Languages, me.Languages)
	if _, err := app.Db.Update("{{users}}", db.H{"languages": langs}, db.H{"id": c.User.Id}); err != nil {
		return apiFail(c.Context, 500, "db")
	}
	return writeMe(c.Context, c.User.Id)
}

func (c *ApiMeController) ActionSettings() error {
	me := users.Current(c.Context)
	var in struct {
		AgeMin       int    `json:"ageMin"`
		AgeMax       int    `json:"ageMax"`
		MaxKm        int    `json:"maxKm"`
		Seeking      string `json:"seeking"`
		VerifiedOnly bool   `json:"verifiedOnly"`
		Invisible    bool   `json:"invisible"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	if in.Invisible && !me.IsPremium() {
		return apiFail(c.Context, 402, "premium", "Невидимка доступна с Premium.")
	}
	users.SaveSettings(me, users.Settings{AgeMin: in.AgeMin, AgeMax: in.AgeMax, MaxKm: in.MaxKm, Seeking: in.Seeking,
		VerifiedOnly: in.VerifiedOnly, Invisible: in.Invisible})
	return writeMe(c.Context, c.User.Id)
}

func (c *ApiMeController) ActionDelete() error {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	if in.Confirm != "УДАЛИТЬ" {
		return apiFail(c.Context, 400, "confirm", "Введите слово УДАЛИТЬ.")
	}
	if err := users.Delete(c.User.Id); err != nil {
		return apiFail(c.Context, 500, "db")
	}
	return c.Json(web.M{"ok": true})
}

// ---------- Фото ----------

func (c *ApiMeController) ActionPhotoUpload() error {
	if len(photos.Mine(c.User.Id)) >= photos.MaxPhotos {
		return apiFail(c.Context, 400, "limit", "Максимум 6 фото.")
	}
	name, err := photos.Store(c.Context, "photo", photos.KindPhoto)
	if err != nil {
		return apiFail(c.Context, 400, "image", err.Error())
	}
	photos.Add(c.User.Id, name)
	return writeMe(c.Context, c.User.Id)
}

func (c *ApiMeController) ActionPhotoDelete() error {
	photos.Remove(c.User.Id, c.GetInt64("id"))
	return writeMe(c.Context, c.User.Id)
}

func (c *ApiMeController) ActionPhotoMain() error {
	photos.SetMain(c.User.Id, c.GetInt64("id"))
	return writeMe(c.Context, c.User.Id)
}

func (c *ApiMeController) ActionVerify() error {
	name, err := photos.Store(c.Context, "selfie", photos.KindPrivate)
	if err != nil {
		return apiFail(c.Context, 400, "image", err.Error())
	}
	users.SetVerified(c.User.Id, name)
	return writeMe(c.Context, c.User.Id)
}

// ---------- Push-токены (регистрация; отправка — этап 5) ----------

func (c *ApiMeController) ActionPushRegister() error {
	var in struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	if !strings.HasPrefix(in.Token, "ExponentPushToken[") && !strings.HasPrefix(in.Token, "ExpoPushToken[") || len(in.Token) > 200 {
		return apiFail(c.Context, 400, "token", "Некорректный токен уведомлений.")
	}
	platform := []rune(in.Platform)
	if len(platform) > 20 {
		platform = platform[:20]
	}
	// токен принадлежит устройству: если на нём вошли другим аккаунтом — переезжает
	if _, err := app.Db.Upsert("{{push_tokens}}", db.H{"token": in.Token, "user_id": c.User.Id, "platform": string(platform), "created_at": time.Now().UTC()}, []string{"user_id", "platform"}, "token"); err != nil {
		return apiFail(c.Context, 500, "db")
	}
	return c.Json(web.M{"ok": true})
}

func (c *ApiMeController) ActionPushUnregister() error {
	var in struct {
		Token string `json:"token"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	app.Db.MustDelete("{{push_tokens}}", db.H{"token": in.Token, "user_id": c.User.Id})
	return c.Json(web.M{"ok": true})
}
