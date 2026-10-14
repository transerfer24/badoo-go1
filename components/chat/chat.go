// Package chat — пары, треды и сообщения (перенос чатовой части handlers_social.go без транспорта).
package chat

import (
	"sort"
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
func Threads(me *users.User) []Thread {
	rows := app.Db.CreateCommand(`
		SELECT x.id, CASE WHEN x.user_a = :me THEN x.user_b ELSE x.user_a END AS other, x.created_at,
		  COALESCE((SELECT body FROM {{messages}} m WHERE m.match_id = x.id ORDER BY m.id DESC LIMIT 1), '') AS body,
		  COALESCE((SELECT sender_id FROM {{messages}} m WHERE m.match_id = x.id ORDER BY m.id DESC LIMIT 1), 0) AS sender,
		  (SELECT created_at FROM {{messages}} m WHERE m.match_id = x.id ORDER BY m.id DESC LIMIT 1) AS at,
		  (SELECT COUNT(*) FROM {{messages}} m WHERE m.match_id = x.id AND m.sender_id <> :me AND m.read_at IS NULL) AS unread
		FROM {{matches}} x WHERE x.user_a = :me OR x.user_b = :me
	`).BindValue(":me", me.Id).MustAll()

	others := make([]int64, 0, len(rows))
	for _, r := range rows {
		others = append(others, r.Int64("other"))
	}
	byId := users.ByIds(others)
	blocked := discovery.BlockedSet(me.Id)
	out := []Thread{}
	for _, r := range rows {
		u := byId[r.Int64("other")]
		if u == nil || blocked[u.Id] {
			continue
		}
		at := r.Time("at")
		if at.IsZero() {
			at = r.Time("created_at")
		}
		out = append(out, Thread{r.Int64("id"), u, r.Str("body"), r.Int64("sender") == me.Id, at, r.Int("unread")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
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
	m := Msg{Id: id, Mine: true, Body: body, At: users.Ts(at)}
	hub.Send(peer.Id, "message", map[string]any{"matchId": matchId, "msg": Msg{Id: id, Body: body, At: users.Ts(at)}, "from": me.Name})
	return m, true
}

// MarkRead — отметить чужие сообщения пары прочитанными; собеседнику — событие read.
func MarkRead(me *users.User, matchId int64) int64 {
	n := app.Db.CreateCommand("UPDATE {{messages}} SET read_at = :now WHERE match_id = :mid AND sender_id <> :me AND read_at IS NULL",
		db.P{":now": time.Now().UTC(), ":mid": matchId, ":me": me.Id}).MustExecute()
	if n > 0 {
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
