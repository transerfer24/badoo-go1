// Package chat — пары, треды и сообщения (перенос чатовой части handlers_social.go без транспорта).
// Выборки — одним SQL в db.Rows (стиль WealthBook); last_msg_id/last_at/unread_* лежат в matches
// (миграция m261015_120000) и обновляются при отправке и прочтении — подзапросов к messages в списках нет.
package chat

import (
	"strings"
	"time"

	"badoo/components/hub"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

func init() { users.UnreadFn = Unread }

// Threads — все пары с собеседником, последним сообщением и непрочитанными, свежие сверху.
// Колонки: match_id, last_at, unread, last, last_mine + анкета собеседника (users.Cols, photo).
func Threads(me *users.User) db.Rows {
	return app.Db.CreateCommand(`
		SELECT x.id AS match_id, x.last_at,
		  CASE WHEN x.user_a = :me THEN x.unread_a ELSE x.unread_b END AS unread,
		  COALESCE(m.body, '') AS last, COALESCE(m.sender_id, 0) = :me AS last_mine,
		  `+users.Cols+`, `+users.Photo+`
		FROM {{matches}} x
		INNER JOIN {{users}} u ON u.id = CASE WHEN x.user_a = :me THEN x.user_b ELSE x.user_a END
		LEFT JOIN {{messages}} m ON m.id = x.last_msg_id
		WHERE (x.user_a = :me OR x.user_b = :me)`+users.NotBlocked+`
		ORDER BY x.last_at DESC
	`, users.P(me)).MustAll()
}

// Peer — собеседник по паре (анкета с фото и расстоянием); nil — я не участник или блокировка.
func Peer(me *users.User, matchId int64) db.Row {
	p := users.P(me)
	p[":mid"] = matchId
	return app.Db.CreateCommand(`SELECT `+users.Cols+`, `+users.Photo+`, `+users.Dist+`, x.id AS match_id
		FROM {{matches}} x INNER JOIN {{users}} u ON u.id = CASE WHEN x.user_a = :me THEN x.user_b ELSE x.user_a END
		WHERE x.id = :mid AND (x.user_a = :me OR x.user_b = :me)`+users.NotBlocked, p).MustRow()
}

// Messages — сообщения пары после id after; ключи как ждут веб и приложение: id, mine, body, at, read.
func Messages(me *users.User, matchId, after int64) db.Rows {
	return app.Db.CreateCommand(`SELECT id, sender_id = :me AS mine, body, created_at AS at, read_at IS NOT NULL AS [[read]]
		FROM {{messages}} WHERE match_id = :mid AND id > :after ORDER BY id LIMIT 500`,
		db.P{":me": me.Id, ":mid": matchId, ":after": after}).MustAll()
}

// Send — сообщение в пару; false — пустое или длиннее 2000 символов. Возвращает строку как в Messages.
func Send(me *users.User, peerId, matchId int64, body string) (db.Row, bool) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > 2000 {
		return nil, false
	}
	at := time.Now().UTC()
	id := app.Db.MustInsert("{{messages}}", db.H{"match_id": matchId, "sender_id": me.Id, "body": body, "created_at": at})
	// шапка пары: последнее сообщение и +1 непрочитанное собеседнику (unread_a — у user_a, unread_b — у user_b)
	app.Db.CreateCommand(`UPDATE {{matches}} SET last_msg_id = :id, last_at = :at,
		unread_a = unread_a + CASE WHEN user_a = :peer THEN 1 ELSE 0 END,
		unread_b = unread_b + CASE WHEN user_b = :peer THEN 1 ELSE 0 END WHERE id = :mid`,
		db.P{":id": id, ":at": at, ":peer": peerId, ":mid": matchId}).MustExecute()
	hub.Send(peerId, "message", map[string]any{"matchId": matchId, "msg": db.Row{"id": id, "mine": false, "body": body, "at": users.Ts(at), "read": false}, "from": me.Name})
	return db.Row{"id": id, "mine": true, "body": body, "at": at, "read": false}, true
}

// MarkRead — отметить чужие сообщения пары прочитанными; собеседнику — событие read.
func MarkRead(me *users.User, matchId int64) int64 {
	n := app.Db.CreateCommand("UPDATE {{messages}} SET read_at = :now WHERE match_id = :mid AND sender_id <> :me AND read_at IS NULL",
		db.P{":now": time.Now().UTC(), ":mid": matchId, ":me": me.Id}).MustExecute()
	if n > 0 {
		app.Db.CreateCommand(`UPDATE {{matches}} SET unread_a = CASE WHEN user_a = :me THEN 0 ELSE unread_a END,
			unread_b = CASE WHEN user_b = :me THEN 0 ELSE unread_b END WHERE id = :mid`, db.P{":me": me.Id, ":mid": matchId}).MustExecute()
		if peer := Peer(me, matchId); peer != nil {
			hub.Send(peer.Int64("id"), "read", map[string]any{"matchId": matchId})
		}
	}
	return n
}

// Unread — непрочитанные по всем парам (счётчик в навигации): SUM по индексам matches, без messages.
func Unread(uid int64) int {
	return int(app.Db.CreateCommand(`SELECT COALESCE(SUM(CASE WHEN user_a = :me THEN unread_a ELSE unread_b END), 0)
		FROM {{matches}} WHERE user_a = :me OR user_b = :me`).BindValue(":me", uid).MustInt())
}

// Rebuild — пересчитать last_msg_id/last_at/unread_* по таблице messages (после импорта данных).
// Принимает транзакцию импорта: отдельное соединение ждало бы её блокировку записи.
func Rebuild(tx *db.Tx) {
	tx.CreateCommand(`UPDATE {{matches}} SET
		last_msg_id = (SELECT MAX(id) FROM {{messages}} WHERE match_id = {{matches}}.id),
		last_at = COALESCE((SELECT MAX(created_at) FROM {{messages}} WHERE match_id = {{matches}}.id), created_at),
		unread_a = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = {{matches}}.id AND sender_id = {{matches}}.user_b AND read_at IS NULL),
		unread_b = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = {{matches}}.id AND sender_id = {{matches}}.user_a AND read_at IS NULL)`).MustExecute()
}

// CanSend — писать могут все или только подтверждённые (amura.chat_verify).
func CanSend(u *users.User) bool {
	need, _ := app.Params["amura.chat_verify"].(bool)
	return !need || u.IsVerified()
}
