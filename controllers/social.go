package controllers

import (
	"fmt"
	"net/http"
	"strconv"
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

	// threads — колонка переписок слева (как в макете dating2): переключаться, не возвращаясь к списку
	return c.Render("chat", web.M{"match_id": mid, "peer": peer, "messages": arr.Map(chat.Messages(me, mid, 0), toMsg),
		"can_send": chat.CanSend(me), "threads": chat.Threads(me), "reasons": discovery.ReportReasons})
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
//
// Ядро закрывает запись через WriteTimeout (60 с), поэтому поток сам завершается через 50 с, а браузер (EventSource)
// переподключается через retry и присылает Last-Event-ID — номер последнего полученного события. Всё, что пришло
// в паузе, досылается из буфера хаба (hub.Since): события не теряются ни при переподключении, ни после выкладки.
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
	uid := c.User.Id
	ch := hub.Subscribe(uid) // сначала подписка, потом добор пропущенного — чтобы ничего не проскочило между ними
	defer hub.Unsubscribe(uid, ch)

	last, _ := strconv.ParseInt(c.Request.Header.Get("Last-Event-ID"), 10, 64)
	write := func(ev hub.Event) {
		if ev.Seq <= last {
			return // уже отправляли (добор из буфера и живая подписка могут совпасть)
		}
		last = ev.Seq
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, ev.Data)
	}
	fmt.Fprint(w, "retry: 2000\n: ok\n\n")
	if last > 0 {
		evs, _ := hub.Since(uid, last)
		for _, ev := range evs {
			write(ev)
		}
	}
	fl.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	end := time.NewTimer(50 * time.Second) // раньше WriteTimeout ядра: закрываемся сами, браузер переподключится
	defer end.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return nil
		case <-end.C:
			return nil
		case ev := <-ch:
			write(ev)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
