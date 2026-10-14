package controllers

import (
	"fmt"
	"net/http"
	"time"

	"badoo/components/chat"
	"badoo/components/discovery"
	"badoo/components/hub"
	"badoo/components/users"

	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
SocialController — симпатии, гости, чаты, события (перенос handlers_social.go + SSE из hub.go + apiCounts).

	GET /likes?tab=you|mutual   GET /visitors   GET /messages   GET /chat/<id>
	GET /api/chat/<id>/messages?after=N   POST /api/chat/<id>/send {body}   POST /api/chat/<id>/read
	GET /api/events (SSE)   GET /api/counts
*/
type SocialController struct{ AppController }

func (c *SocialController) Init() {
	c.NoOnboard = []string{"events", "counts"} // как a.auth(false, …): лейаут дёргает их и до онбординга
}

func (c *SocialController) ActionLikes() error {
	me := users.Current(c.Context)
	tab := c.Get("tab")
	if tab != "mutual" {
		tab = "you"
	}
	likers := discovery.WithDistance(me, `SELECT u.* FROM {{votes}} v INNER JOIN {{users}} u ON u.id = v.voter_id
		WHERE v.target_id = :me AND v.liked = 1
		AND NOT EXISTS (SELECT 1 FROM {{votes}} m WHERE m.voter_id = :me AND m.target_id = v.voter_id)`+discovery.NotBlocked+`
		ORDER BY v.created_at DESC LIMIT 200`, db.P{":me": me.Id})
	matches := discovery.WithDistance(me, `SELECT u.* FROM {{matches}} x INNER JOIN {{users}} u ON u.id = CASE WHEN x.user_a = :me THEN x.user_b ELSE x.user_a END
		WHERE (x.user_a = :me OR x.user_b = :me)`+discovery.NotBlocked+` ORDER BY x.created_at DESC LIMIT 200`, db.P{":me": me.Id})
	c.View.Params = web.M{"active": "likes"}

	return c.Render("likes", web.M{"tab": tab, "likers": likers, "matches": matches, "premium": me.IsPremium()})
}

type visitor struct {
	U  *users.User
	At time.Time
}

func (c *SocialController) ActionVisitors() error {
	me := users.Current(c.Context)
	rows := c.Db().CreateCommand(`SELECT v.visitor_id, v.at FROM {{visits}} v INNER JOIN {{users}} u ON u.id = v.visitor_id
		WHERE v.visited_id = :me AND v.at > :since`+discovery.NotBlocked+` ORDER BY v.at DESC LIMIT 100`,
		db.P{":me": me.Id, ":since": time.Now().AddDate(0, 0, -30).UTC()}).MustAll()
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.Int64("visitor_id"))
	}
	byId := users.ByIds(ids)
	list := []visitor{}
	for _, r := range rows {
		if u := byId[r.Int64("visitor_id")]; u != nil {
			u.DistKm = users.HaversineKm(me.Lat, me.Lng, u.Lat, u.Lng)
			list = append(list, visitor{u, r.Time("at")})
		}
	}
	c.View.Params = web.M{"active": "likes"}
	return c.Render("visitors", web.M{"visitors": list})
}

func (c *SocialController) ActionMessages() error {
	me := users.Current(c.Context)
	fresh, active := []chat.Thread{}, []chat.Thread{}
	for _, t := range chat.Threads(me) {
		if t.Last == "" {
			fresh = append(fresh, t)
		} else {
			active = append(active, t)
		}
	}
	c.View.Params = web.M{"active": "messages"}
	return c.Render("messages", web.M{"new": fresh, "active": active})
}

func (c *SocialController) ActionChat() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	peer := chat.Peer(me, mid)
	if peer == nil {
		return c.NotFound()
	}
	peer.DistKm = users.HaversineKm(me.Lat, me.Lng, peer.Lat, peer.Lng)
	chat.MarkRead(me, mid)
	c.View.SetTitle(peer.Name)
	c.View.Params = web.M{"active": "messages"}

	return c.Render("chat", web.M{"match_id": mid, "peer": peer, "messages": chat.Messages(me, mid, 0), "can_send": chat.CanSend(me)})
}

func (c *SocialController) ActionChatMessages() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	if chat.Peer(me, mid) == nil {
		return c.NotFound()
	}
	return c.Json(chat.Messages(me, mid, c.GetInt64("after")))
}

func (c *SocialController) ActionChatSend() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	peer := chat.Peer(me, mid)
	if peer == nil {
		return c.NotFound()
	}
	if !chat.CanSend(me) {
		c.SetStatus(403)
		return c.Json(web.M{"error": "verify"})
	}
	m, ok := chat.Send(me, peer, mid, c.Post("body"))
	if !ok {
		return web.ErrBadRequest("empty or too long")
	}
	return c.Json(m)
}

func (c *SocialController) ActionChatRead() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	if chat.Peer(me, mid) == nil {
		return c.NotFound()
	}
	return c.Json(web.M{"marked": chat.MarkRead(me, mid)})
}

// Счётчики для навигации (ключи с заглавной — app.js читает c.Likes / c.Unread).
func (c *SocialController) ActionCounts() error {
	return c.Json(users.Counts(c.Context))
}

// SSE: события пользователя в реальном времени для веб-версии (приложение ходит long-poll в API v1).
func (c *SocialController) ActionEvents() error {
	w := c.Response
	fl, ok := w.(http.Flusher)
	if !ok {
		return web.NewHttpError(500, "streaming unsupported")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	c.Hijacked(200) // ответ пишем сами, ядро после возврата ничего не дописывает
	ch := hub.Subscribe(c.User.Id)
	defer hub.Unsubscribe(c.User.Id, ch)
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return nil
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
