package controllers

import (
	"time"

	"badoo/components/chat"
	"badoo/components/discovery"
	"badoo/components/hub"
	"badoo/components/users"

	"whales.team/go1/helpers/arr"
	"whales.team/go1/web"
)

/*
ApiSocialController — симпатии, гости, чаты и события в приложении (перенос части api_v1.go):

	GET  /api/v1/likes                      {premium, likesCount, matches, likers | lockedColors}
	GET  /api/v1/visitors                   {visitors: [{user, at}]}
	GET  /api/v1/counts                     {likes, unread}
	GET  /api/v1/chats                      {chats: [threadDTO]}
	GET  /api/v1/chats/<id>                 {matchId, peer, messages, canSend}  (+ отмечает прочитанным)
	GET  /api/v1/chats/<id>/messages?after  [Msg]
	POST /api/v1/chats/<id>/messages {body} Msg; 403 verify — чат только подтверждённым
	POST /api/v1/chats/<id>/read            {marked}
	GET  /api/v1/events[?since=N]           {events, seq} — long-poll до 25 с
*/
type ApiSocialController struct{ ApiController }

func (c *ApiSocialController) Init() { c.NoOnboard = []string{"events"} }

func (c *ApiSocialController) ActionLikes() error {
	me := users.Current(c.Context)
	likers := discovery.Likers(me)
	out := web.M{"premium": me.IsPremium(), "likesCount": len(likers), "matches": toDTOs(discovery.Matches(me)), "likers": []UserDTO{}}
	if me.IsPremium() {
		out["likers"] = toDTOs(likers)
	} else {
		// без Premium — только цвета для размытых плиток, без лиц и имён
		colors := make([]int, 0, len(likers))
		for _, u := range likers {
			colors = append(colors, users.ColorIndex(u.Id))
		}
		out["lockedColors"] = colors
	}
	return c.Json(out)
}

func (c *ApiSocialController) ActionVisitors() error {
	type visit struct {
		User UserDTO `json:"user"`
		At   string  `json:"at"`
	}
	return c.Json(web.M{"visitors": arr.Map(discovery.Visitors(users.Current(c.Context)), func(v discovery.Visitor) visit {
		return visit{toDTO(v.U), users.Ts(v.At)}
	})})
}

func (c *ApiSocialController) ActionCounts() error {
	n := users.Counts(c.Context)
	return c.Json(web.M{"likes": n.Likes, "unread": n.Unread})
}

type threadDTO struct {
	MatchId  int64   `json:"matchId"`
	User     UserDTO `json:"user"`
	Last     string  `json:"last"`
	LastMine bool    `json:"lastMine"`
	LastAt   string  `json:"lastAt"`
	Unread   int     `json:"unread"`
}

func (c *ApiSocialController) ActionChats() error {
	return c.Json(web.M{"chats": arr.Map(chat.Threads(users.Current(c.Context)), toThread)})
}

func toThread(t chat.Thread) threadDTO {
	return threadDTO{t.MatchId, toDTO(t.U), t.Last, t.LastMine, users.Ts(t.LastAt), t.Unread}
}

func (c *ApiSocialController) ActionChat() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	peer := chat.Peer(me, mid)
	if peer == nil {
		return apiFail(c.Context, 404, "not_found", "Чат недоступен.")
	}
	peer.DistKm = users.HaversineKm(me.Lat, me.Lng, peer.Lat, peer.Lng)
	chat.MarkRead(me, mid)
	return c.Json(web.M{"matchId": mid, "peer": toDTO(peer), "messages": chat.Messages(me, mid, 0), "canSend": chat.CanSend(me)})
}

func (c *ApiSocialController) ActionChatMessages() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	if chat.Peer(me, mid) == nil {
		return apiFail(c.Context, 404, "not_found", "Чат недоступен.")
	}
	return c.Json(chat.Messages(me, mid, c.GetInt64("after")))
}

func (c *ApiSocialController) ActionChatSend() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	peer := chat.Peer(me, mid)
	if peer == nil {
		return apiFail(c.Context, 404, "not_found", "Чат недоступен.")
	}
	if !chat.CanSend(me) {
		return apiFail(c.Context, 403, "verify", "Чтобы писать, подтвердите фото.")
	}
	var in struct {
		Body string `json:"body"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	msg, ok := chat.Send(me, peer, mid, in.Body)
	if !ok {
		return apiFail(c.Context, 400, "body", "Сообщение пустое или слишком длинное.")
	}
	return c.Json(msg)
}

func (c *ApiSocialController) ActionChatRead() error {
	me := users.Current(c.Context)
	mid := c.GetInt64("id")
	if chat.Peer(me, mid) == nil {
		return apiFail(c.Context, 404, "not_found", "Чат недоступен.")
	}
	return c.Json(web.M{"marked": chat.MarkRead(me, mid)})
}

// GET /api/v1/events — текущий номер; ?since=N — long-poll до 25 секунд. {"events": [...], "seq": N}.
func (c *ApiSocialController) ActionEvents() error {
	uid := c.User.Id
	since := c.GetInt64("since")
	ch := hub.Subscribe(uid)
	defer hub.Unsubscribe(uid, ch)
	evs, seq := hub.Since(uid, since)
	if !c.Request.URL.Query().Has("since") {
		// первый запрос (без since): только узнаём текущий номер, старые события не нужны
		return c.Json(web.M{"events": []hub.Event{}, "seq": seq})
	}
	if len(evs) == 0 {
		select {
		case <-ch:
			time.Sleep(50 * time.Millisecond) // собрать пачку, если событий несколько
			evs, seq = hub.Since(uid, since)
		case <-time.After(25 * time.Second):
		case <-c.Request.Context().Done():
			return nil
		}
	}
	if evs == nil {
		evs = []hub.Event{}
	}
	return c.Json(web.M{"events": evs, "seq": seq})
}
