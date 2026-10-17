package users

// Модерация из админки /control: блокировка, верификация, Premium. SQL — здесь, контроллеры админки только
// разбирают вход, пишут журнал (control_log) и отвечают JSON.

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"badoo/components/photos"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

/*
Ban — заблокировать: banned = 1 и выход на всех устройствах.

  - auth.Finder не находит заблокированного → сайт считает гостем (cookie-сессия и remember-me перестают работать,
    auth_key меняется), API отвечает 401 (Bearer-токены удалены);
  - push-токены удалены — уведомления не приходят;
  - NotBlocked / goingNotBlocked прячут его из колоды, «Рядом», симпатий, пар, чатов и встреч;
  - непрочитанные от него у собеседников обнуляются (чатов с ним не видно — счётчик не должен висеть);
  - войти заново не даст neuronet.syncUser (покажет причину).
*/
func Ban(uid, adminId int64, reason string) {
	now := time.Now().UTC()
	app.Db.Transaction(func(tx *db.Tx) error {
		tx.MustUpdate("{{users}}", db.H{"banned": true, "ban_reason": reason, "banned_at": now, "banned_by": adminId, "auth_key": newKey()}, db.H{"id": uid})
		tx.MustDelete("{{sessions}}", db.H{"user_id": uid})
		tx.MustDelete("{{push_tokens}}", db.H{"user_id": uid})
		tx.CreateCommand("UPDATE {{matches}} SET unread_a = 0 WHERE user_b = :uid", db.P{":uid": uid}).MustExecute()
		tx.CreateCommand("UPDATE {{matches}} SET unread_b = 0 WHERE user_a = :uid", db.P{":uid": uid}).MustExecute()
		return nil
	})
}

// Unban — снять блокировку (войдёт заново через Neuronet).
func Unban(uid int64) {
	app.Db.MustUpdate("{{users}}", db.H{"banned": false, "ban_reason": "", "banned_at": nil, "banned_by": nil}, db.H{"id": uid})
}

// Logout — выйти на всех устройствах, не блокируя (сменить auth_key, удалить Bearer-токены).
func Logout(uid int64) int64 {
	app.Db.MustUpdate("{{users}}", db.H{"auth_key": newKey()}, db.H{"id": uid})
	n, _ := app.Db.Delete("{{sessions}}", db.H{"user_id": uid})
	return n
}

// Verify — решение по селфи: ok → подтверждён (2), иначе — снова «не подтверждён» (0) и селфи удаляется.
func Verify(uid int64, ok bool) {
	if ok {
		app.Db.MustUpdate("{{users}}", db.H{"verified": 2}, db.H{"id": uid})
		return
	}
	if f := app.Db.CreateCommand("SELECT verify_file FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustString(); f != "" {
		photos.Delete(f)
	}
	app.Db.MustUpdate("{{users}}", db.H{"verified": 0, "verify_file": ""}, db.H{"id": uid})
}

// GrantPremium — продлить Premium на days дней от текущего окончания (или от сейчас); days <= 0 — отключить.
func GrantPremium(uid int64, days int) {
	if days <= 0 {
		app.Db.MustUpdate("{{users}}", db.H{"premium_until": nil, "invisible": false}, db.H{"id": uid})
		return
	}
	from := time.Now().UTC()
	if t := app.Db.CreateCommand("SELECT premium_until FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustRow().Time("premium_until"); t.After(from) {
		from = t.UTC()
	}
	app.Db.MustUpdate("{{users}}", db.H{"premium_until": from.AddDate(0, 0, days)}, db.H{"id": uid})
}

func newKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
