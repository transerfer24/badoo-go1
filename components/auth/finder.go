// Package auth — кто такой текущий пользователь: identity для web.AuthConfig.
//
// Веб-версия входит через cookie-сессию Go1 (c.User.Login(identity)), мобильное приложение —
// по Bearer-токену из таблицы sessions (как в старом бэкенде: Authorization: Bearer <token>).
// Пароли здесь не хранятся: вход идёт через Neuronet (components/neuronet, этап 2 плана).
package auth

import (
	"time"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/helpers/security"
	"whales.team/go1/web"
)

// User — identity: только то, что нужно на каждом запросе. Полная анкета — отдельным запросом в экшене.
type User struct {
	Id       int64
	Email    string
	Name     string
	Onboard  bool // анкета заполнена (onboarded = 1)
	Verified int  // 0 нет, 1 на проверке, 2 подтверждён
	Premium  bool
	AuthKey  string
}

func (u User) GetId() int64       { return u.Id }
func (u User) GetAuthKey() string { return u.AuthKey } // remember-me cookie веб-версии

type Finder struct{}

// FindIdentity — по id из сессии / cookie.
func (Finder) FindIdentity(id int64) (web.Identity, error) {
	row, err := app.Db.CreateCommand("SELECT id, email, name, onboarded, verified, premium_until, auth_key FROM {{users}} WHERE id = :id").
		BindValue(":id", id).
		QueryRow()
	if err != nil || row == nil {
		return nil, err
	}
	return fromRow(row), nil
}

// FindIdentityByAccessToken — по Bearer-токену мобильного приложения (таблица sessions, как раньше).
func (Finder) FindIdentityByAccessToken(token string) (web.Identity, error) {
	row, err := app.Db.CreateCommand(`
		SELECT u.id, u.email, u.name, u.onboarded, u.verified, u.premium_until, u.auth_key
		FROM {{sessions}} s INNER JOIN {{users}} u ON u.id = s.user_id
		WHERE s.token = :token AND s.expires_at > :now
	`).
		BindValue(":token", token).
		BindValue(":now", time.Now().UTC()).
		QueryRow()
	if err != nil || row == nil {
		return nil, err
	}
	return fromRow(row), nil
}

func fromRow(row db.Row) User {
	premium := row.Time("premium_until")
	return User{
		Id:       row.Int64("id"),
		Email:    row.Str("email"),
		Name:     row.Str("name"),
		Onboard:  row.Bool("onboarded"),
		Verified: row.Int("verified"),
		Premium:  !premium.IsZero() && premium.After(time.Now()),
		AuthKey:  row.Str("auth_key"),
	}
}

// TokenTTL — срок Bearer-токена мобильного приложения (как sessionTTL старого бэкенда).
const TokenTTL = 90 * 24 * time.Hour

// NewToken — выдать Bearer-токен приложению после входа через Neuronet (/api/v1/auth/*).
func NewToken(uid int64, platform string) (string, error) {
	tok := security.RandomString(43) // 32 байта в base64 — та же длина, что раньше
	now := time.Now().UTC()
	// CreateCommand, а не Insert: у sessions нет автоинкремента, Insert ждал бы RETURNING id
	_, err := app.Db.CreateCommand(`INSERT INTO {{sessions}} (token, user_id, platform, expires_at, created_at)
		VALUES (:token, :uid, :platform, :exp, :now)`, db.P{":token": tok, ":uid": uid, ":platform": platform, ":exp": now.Add(TokenTTL), ":now": now}).Execute()
	return tok, err
}

// DropToken — выход в приложении.
func DropToken(token string, uid int64) {
	app.Db.MustDelete("{{sessions}}", db.H{"token": token, "user_id": uid})
}
