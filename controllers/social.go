package controllers

import (
	"fmt"
	"net/http"
	"time"

	"badoo/components/chat"
	"badoo/components/discovery"
	"badoo/components/hub"
	"badoo/components/users"

	"whales.team/go1/helpers/arr"
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
	likers, matches := discovery.Likers(me), discovery.Matches(me)
	c.View.Params = web.M{"active": "likes"}

	return c.Render("likes", web.M{"tab": tab, "likers": likers, "matches": matches, "premium": me.IsPremium()})
}

func (c *SocialController) ActionVisitors() error {
	me := users.Current(c.Context)
	list := discovery.Visitors(me)
	c.View.Params = web.M{"active": "likes"}
	return c.Render("visitors", web.M{"visitors": list})
}

func (c *SocialController) ActionMessages() error {
	c.View.Params = web.M{"active": "messages"}
	return c.Render("messages", web.M{"threads": chat.Threads(users.Current(c.Context))}) // новые пары и переписка — в шаблоне по .last
}

func (c *SocialController) ActionChat() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	peer := chat.Peer(me, mid)
	if peer == nil {
		return c.NotFound()
	}
	chat.MarkRead(me, mid)
	c.View.SetTitle(peer.Str("name"))
	c.View.Params = web.M{"active": "messages"}

	return c.Render("chat", web.M{"match_id": mid, "peer": peer, "messages": arr.Map(chat.Messages(me, mid, 0), toMsg), "can_send": chat.CanSend(me)})
}

func (c *SocialController) ActionChatMessages() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	if chat.Peer(me, mid) == nil {
		return c.NotFound()
	}
	return c.Json(arr.Map(chat.Messages(me, mid, c.GetInt64("after")), toMsg))
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
	m, ok := chat.Send(me, peer.Int64("id"), mid, c.Post("body"))
	if !ok {
		return web.ErrBadRequest("empty or too long")
	}
	return c.Json(toMsg(m))
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
