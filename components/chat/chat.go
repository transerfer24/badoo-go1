// Package chat — пары, треды и сообщения (перенос чатовой части handlers_social.go без транспорта).
package chat

import (
	"strings"
	"time"

	"badoo/components/discovery"
	"badoo/components/hub"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

type Thread struct {
	MatchId  int64
	U        *users.User
	Last     string
	LastMine bool
	LastAt   time.Time
	Unread   int
}

// Threads — все пары пользователя с последним сообщением и непрочитанными, свежие сверху.
// Как в Yii: queryAll → getColumn → findAll(IN) → склейка. last_msg_id/last_at/unread_* лежат в matches
// (миграция m261015_120000), подзапросов к messages нет.
func Threads(me *users.User) []Thread {
	rows := app.Db.CreateCommand(`
		SELECT x.id, x.last_at,
		  CASE WHEN x.user_a = :me THEN x.user_b ELSE x.user_a END AS other,
		  CASE WHEN x.user_a = :me THEN x.unread_a ELSE x.unread_b END AS unread,
		  COALESCE(m.body, '') AS body, COALESCE(m.sender_id, 0) AS sender
		FROM {{matches}} x LEFT JOIN {{messages}} m ON m.id = x.last_msg_id
		WHERE (x.user_a = :me OR x.user_b = :me)`+discovery.NotBlockedExpr("CASE WHEN x.user_a = :me THEN x.user_b ELSE x.user_a END")+`
		ORDER BY x.last_at DESC
	`).BindValue(":me", me.Id).MustAll()
	people := users.ByIds(rows.IntColumn("other")) // анкеты собеседников одним IN, map по id

	out := []Thread{}
	for _, r := range rows {
		if u := people[r.Int64("other")]; u != nil {
			out = append(out, Thread{
				MatchId: r.Int64("id"), U: u, Last: r.Str("body"), LastMine: r.Int64("sender") == me.Id,
				LastAt: r.Time("last_at"), Unread: r.Int("unread"),
			})
		}
	}
	return out
}

func init() { users.UnreadFn = Unread }

// Rebuild — пересчитать last_msg_id/last_at/unread_* по таблице messages (после импорта данных).
// Принимает транзакцию импорта: отдельное соединение ждало бы её блокировку записи.
func Rebuild(tx *db.Tx) {
	tx.CreateCommand(`UPDATE {{matches}} SET
		last_msg_id = (SELECT MAX(id) FROM {{messages}} WHERE match_id = {{matches}}.id),
		last_at = COALESCE((SELECT MAX(created_at) FROM {{messages}} WHERE match_id = {{matches}}.id), created_at),
		unread_a = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = {{matches}}.id AND sender_id = {{matches}}.user_b AND read_at IS NULL),
		unread_b = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = {{matches}}.id AND sender_id = {{matches}}.user_a AND read_at IS NULL)`).MustExecute()
}

// Unread — непрочитанные у пользователя по всем парам (счётчик в навигации): SUM по двум индексам, без messages.
func Unread(uid int64) int {
	return int(app.Db.CreateCommand(`SELECT COALESCE(SUM(CASE WHEN user_a = :me THEN unread_a ELSE unread_b END), 0)
		FROM {{matches}} WHERE user_a = :me OR user_b = :me`).BindValue(":me", uid).MustInt())
}

type Msg struct {
	Id   int64  `json:"id"`
	Mine bool   `json:"mine"`
	Body string `json:"body"`
	At   string `json:"at"`
	Read bool   `json:"read"`
}

// Peer — собеседник по паре, если я её участник и нет блокировки.
func Peer(me *users.User, matchId int64) *users.User {
	row := app.Db.CreateCommand("SELECT user_a, user_b FROM {{matches}} WHERE id = :id").BindValue(":id", matchId).MustRow()
	if row == nil {
		return nil
	}
	ua, ub := row.Int64("user_a"), row.Int64("user_b")
	other := ua
	if ua == me.Id {
		other = ub
	} else if ub != me.Id {
		return nil
	}
	if discovery.BlockedEither(me.Id, other) {
		return nil
	}
	return users.ById(other)
}

func Messages(me *users.User, matchId, after int64) []Msg {
	rows := app.Db.CreateCommand("SELECT id, sender_id, body, created_at, read_at FROM {{messages}} WHERE match_id = :mid AND id > :after ORDER BY id LIMIT 500").
		BindValue(":mid", matchId).BindValue(":after", after).MustAll()
	out := []Msg{}
	for _, r := range rows {
		out = append(out, Msg{Id: r.Int64("id"), Mine: r.Int64("sender_id") == me.Id, Body: r.Str("body"), At: users.Ts(r.Time("created_at")), Read: !r.IsNull("read_at")})
	}
	return out
}

// Send — сообщение в пару; "" — пустое или длиннее 2000 символов.
func Send(me *users.User, peer *users.User, matchId int64, body string) (Msg, bool) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > 2000 {
		return Msg{}, false
	}
	at := time.Now().UTC()
	id := app.Db.MustInsert("{{messages}}", db.H{"match_id": matchId, "sender_id": me.Id, "body": body, "created_at": at})
	// шапка пары: последнее сообщение и +1 непрочитанное собеседнику (unread_a — у user_a, unread_b — у user_b)
	app.Db.CreateCommand(`UPDATE {{matches}} SET last_msg_id = :id, last_at = :at,
		unread_a = unread_a + CASE WHEN user_a = :peer THEN 1 ELSE 0 END,
		unread_b = unread_b + CASE WHEN user_b = :peer THEN 1 ELSE 0 END WHERE id = :mid`,
		db.P{":id": id, ":at": at, ":peer": peer.Id, ":mid": matchId}).MustExecute()
	m := Msg{Id: id, Mine: true, Body: body, At: users.Ts(at)}
	hub.Send(peer.Id, "message", map[string]any{"matchId": matchId, "msg": Msg{Id: id, Body: body, At: users.Ts(at)}, "from": me.Name})
	return m, true
}

// MarkRead — отметить чужие сообщения пары прочитанными; собеседнику — событие read.
func MarkRead(me *users.User, matchId int64) int64 {
	n := app.Db.CreateCommand("UPDATE {{messages}} SET read_at = :now WHERE match_id = :mid AND sender_id <> :me AND read_at IS NULL",
		db.P{":now": time.Now().UTC(), ":mid": matchId, ":me": me.Id}).MustExecute()
	if n > 0 {
		app.Db.CreateCommand(`UPDATE {{matches}} SET unread_a = CASE WHEN user_a = :me THEN 0 ELSE unread_a END,
			unread_b = CASE WHEN user_b = :me THEN 0 ELSE unread_b END WHERE id = :mid`, db.P{":me": me.Id, ":mid": matchId}).MustExecute()
		if peer := Peer(me, matchId); peer != nil {
			hub.Send(peer.Id, "read", map[string]any{"matchId": matchId})
		}
	}
	return n
}

// CanSend — писать могут все или только подтверждённые (amura.chat_verify).
func CanSend(u *users.User) bool {
	need, _ := app.Params["amura.chat_verify"].(bool)
	return !need || u.IsVerified()
}
