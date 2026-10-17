package control

import (
	"strconv"
	"strings"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
ChatsController — переписка: все пары с последним сообщением, поиск по участнику и по тексту сообщений,
просмотр диалога целиком и удаление сообщения (модерация). Читает напрямую из matches/messages —
пользователю ничего не отмечается прочитанным.
*/
type ChatsController struct{ web.Controller }

func (c *ChatsController) Behaviors() []web.Behavior { return access("chats", "index", "view") }

func (c *ChatsController) Init() { c.Layout = "control" }

func (c *ChatsController) ActionIndex() error {
	return c.Render("chats", web.M{"section": "chats", "uid": c.GetInt64("uid")})
}

func (c *ChatsController) ActionView() error {
	id := c.GetInt64("id")
	if app.Db.CreateCommand("SELECT id FROM {{matches}} WHERE id = :id").BindValue(":id", id).MustInt() == 0 {
		return c.Redirect("/control/chats")
	}
	return c.Render("view", web.M{"section": "chats", "id": id})
}

// Пары: uid — только его, text — есть сообщение с этим текстом, empty=0 — только с перепиской
func (c *ChatsController) ActionGetChats() error {
	res := web.M{}
	q := app.Db.Query().
		Select("x.id", "x.created_at", "x.last_at",
			"a.id AS a_id", "a.name AS a_name", "a.banned AS a_banned",
			"b.id AS b_id", "b.name AS b_name", "b.banned AS b_banned",
			"(SELECT p.file FROM {{photos}} p WHERE p.user_id = a.id ORDER BY p.pos, p.id LIMIT 1) AS a_photo",
			"(SELECT p.file FROM {{photos}} p WHERE p.user_id = b.id ORDER BY p.pos, p.id LIMIT 1) AS b_photo",
			"(SELECT COUNT(*) FROM {{messages}} m WHERE m.match_id = x.id) AS messages",
			"COALESCE(lm.body, '') AS last_body", "COALESCE(lm.sender_id, 0) AS last_sender").
		From("{{matches}} x").
		InnerJoin("{{users}} a", db.Raw("a.id = x.user_a")).
		InnerJoin("{{users}} b", db.Raw("b.id = x.user_b")).
		LeftJoin("{{messages}} lm", db.Raw("lm.id = x.last_msg_id"))

	if uid := c.PostInt64("uid"); uid > 0 {
		q.AndWhere(db.Raw("(x.user_a = :uid OR x.user_b = :uid)", db.P{":uid": uid}))
	}
	if s := strings.TrimSpace(c.Post("user")); s != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
		q.AndWhere(db.Raw("(a.name LIKE :us OR a.email LIKE :us OR b.name LIKE :us OR b.email LIKE :us OR a.id = :uid2 OR b.id = :uid2)",
			db.P{":us": "%" + s + "%", ":uid2": id}))
	}
	if t := strings.TrimSpace(c.Post("text")); t != "" {
		q.AndWhere(db.Raw("EXISTS (SELECT 1 FROM {{messages}} tm WHERE tm.match_id = x.id AND tm.body LIKE :t)", db.P{":t": "%" + t + "%"}))
	}
	if c.Post("empty") == "0" {
		q.AndWhere(db.Raw("x.last_msg_id IS NOT NULL"))
	}
	if t := since(c.Post("period")); !t.IsZero() {
		q.AndWhere(db.Raw("x.last_at >= :since", db.P{":since": t}))
	}
	q.OrderBy("x.last_at DESC", "x.id DESC")

	p, err := paginate(q, max(1, c.PostInt("page", 1)), 50)
	if err != nil {
		app.Log.Error("control: список чатов", "err", err)
		res["status"] = 200
		res["items"] = db.Rows{}
		return c.Json(res)
	}
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Сообщения поиска: text — найти по всем диалогам (с собеседниками), для модерации спама и оскорблений
func (c *ChatsController) ActionSearchMessages() error {
	res := web.M{}
	t := strings.TrimSpace(c.Post("text"))
	if len([]rune(t)) < 2 {
		res["status"] = 200
		res["items"] = db.Rows{}
		return c.Json(res)
	}
	p := mustPaginate(app.Db.Query().
		Select("m.id", "m.match_id", "m.body", "m.created_at", "m.sender_id", "s.name AS sender_name", "s.banned AS sender_banned").
		From("{{messages}} m").
		InnerJoin("{{users}} s", db.Raw("s.id = m.sender_id")).
		Where(db.Raw("m.body LIKE :t", db.P{":t": "%" + t + "%"})).
		OrderBy("m.id DESC"), max(1, c.PostInt("page", 1)), 50)
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Диалог: участники и сообщения (before — id, старше которого подгрузить; 0 — последние)
func (c *ChatsController) ActionGetMessages() error {
	res := web.M{}
	id := c.PostInt64("id")
	pair := app.Db.CreateCommand(`
		SELECT x.id, x.created_at, x.user_a, x.user_b,
		       a.name AS a_name, a.email AS a_email, a.banned AS a_banned, a.verified AS a_verified, a.last_seen AS a_last_seen,
		       b.name AS b_name, b.email AS b_email, b.banned AS b_banned, b.verified AS b_verified, b.last_seen AS b_last_seen,
		       (SELECT p.file FROM {{photos}} p WHERE p.user_id = a.id ORDER BY p.pos, p.id LIMIT 1) AS a_photo,
		       (SELECT p.file FROM {{photos}} p WHERE p.user_id = b.id ORDER BY p.pos, p.id LIMIT 1) AS b_photo,
		       (SELECT COUNT(*) FROM {{messages}} m WHERE m.match_id = x.id) AS total,
		       EXISTS (SELECT 1 FROM {{blocks}} k WHERE (k.blocker_id = x.user_a AND k.blocked_id = x.user_b) OR (k.blocker_id = x.user_b AND k.blocked_id = x.user_a)) AS blocked
		FROM {{matches}} x
		INNER JOIN {{users}} a ON a.id = x.user_a
		INNER JOIN {{users}} b ON b.id = x.user_b
		WHERE x.id = :id
	`).BindValue(":id", id).MustRow()
	if pair == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Пара не найдена"}
		return c.Json(res)
	}
	before := c.PostInt64("before")
	if before <= 0 {
		before = 1 << 62
	}
	limit := 100
	items := app.Db.CreateCommand(`SELECT id, sender_id, body, created_at, read_at FROM {{messages}}
		WHERE match_id = :id AND id < :before ORDER BY id DESC LIMIT `+strconv.Itoa(limit+1)).
		BindValue(":id", id).BindValue(":before", before).MustAll()
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	// старые сверху
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	res["status"] = 200
	res["pair"] = prepareRow(pair)
	res["items"] = prepare(items)
	res["has_more"] = hasMore
	return c.Json(res)
}

// Удалить сообщение (оскорбление, спам, контакты мошенника). Шапка пары пересчитывается.
func (c *ChatsController) ActionDeleteMessage() error {
	res := web.M{}
	msg := app.Db.CreateCommand("SELECT id, match_id, sender_id, body FROM {{messages}} WHERE id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if msg == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Сообщение не найдено"}
		return c.Json(res)
	}
	mid := msg.Int64("match_id")
	app.Db.Transaction(func(tx *db.Tx) error {
		tx.MustDelete("{{messages}}", db.H{"id": msg.Int64("id")})
		tx.CreateCommand(`UPDATE {{matches}} SET
			last_msg_id = (SELECT MAX(id) FROM {{messages}} WHERE match_id = :mid),
			last_at = COALESCE((SELECT MAX(created_at) FROM {{messages}} WHERE match_id = :mid), created_at),
			unread_a = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = :mid AND sender_id = {{matches}}.user_b AND read_at IS NULL),
			unread_b = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = :mid AND sender_id = {{matches}}.user_a AND read_at IS NULL)
			WHERE id = :mid`, db.P{":mid": mid}).MustExecute()
		return nil
	})
	body := msg.Str("body")
	if len([]rune(body)) > 200 {
		body = string([]rune(body)[:200]) + "…"
	}
	logAction(c.Context, msg.Int64("sender_id"), "message-delete", "пара #"+strconv.FormatInt(mid, 10)+": "+body)
	res["status"] = 200
	return c.Json(res)
}
