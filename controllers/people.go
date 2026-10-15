package controllers

import (
	"strconv"
	"strings"

	"badoo/components/discovery"
	"badoo/components/users"

	"whales.team/go1/web"
)

/*
PeopleController — «Рядом» и чужая анкета (перенос handleNearby/handleProfile/handleProfileVote/handleBlock/handleReport).

	GET  /nearby               сетка людей рядом с фильтрами в query
	GET  /u/<id>               анкета
	POST /u/<id>/vote          liked=0|1 (+back)
	POST /u/<id>/block
	POST /u/<id>/report        reason + block=1
*/
type PeopleController struct{ AppController }

func (c *PeopleController) ActionNearby() error {
	me := users.Current(c.Context)
	f := discovery.DefaultFilter(me)
	if v := c.GetInt("age_min"); v >= 18 && v <= 99 {
		f.AgeMin = v
	}
	if v := c.GetInt("age_max"); v >= 18 && v <= 99 {
		f.AgeMax = v
	}
	if f.AgeMax < f.AgeMin {
		f.AgeMin, f.AgeMax = f.AgeMax, f.AgeMin
	}
	if v := c.GetInt("km"); v >= 1 && v <= 300 {
		f.MaxKm = v
	}
	if s := c.Get("seeking"); s == "m" || s == "f" || s == "any" {
		f.Seeking = s
	}
	if c.Get("applied") != "" {
		f.VerifiedOnly = c.Get("verified") == "1"
		f.OnlineOnly = c.Get("online") == "1"
	}
	people := discovery.Nearby(me, f)
	c.View.Params = web.M{"active": "nearby"}

	return c.Render("nearby", web.M{"people": people, "f": f})
}

func (c *PeopleController) ActionProfile() error {
	me := users.Current(c.Context)
	id := c.GetInt64("id")
	u := discovery.Profile(me, id) // анкета + фото + расстояние + liked_me/my_vote/match_id одним запросом
	if u == nil {
		return c.NotFound()
	}
	if id != me.Id {
		discovery.Visit(me, id)
	}
	c.View.SetTitle(u.Str("name"))
	return c.Render("profile", web.M{"u": u, "liked_me": u.Bool("liked_me") && me.IsPremium(), "reasons": discovery.ReportReasons, "is_me": id == me.Id})
}

func (c *PeopleController) ActionVote() error {
	id := c.GetInt64("id")
	res, ok := discovery.Vote(users.Current(c.Context), id, c.Post("liked") == "1")
	if !ok {
		return c.NotFound()
	}
	if res.Match {
		c.Session.SetFlash("ok", "Это взаимно! Напишите первым.")
		return c.Redirect("/chat/" + strconv.FormatInt(res.MatchId, 10))
	}
	back := c.Post("back")
	if back == "" || !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		back = "/u/" + strconv.FormatInt(id, 10)
	}
	return c.Redirect(back)
}

func (c *PeopleController) ActionBlock() error {
	discovery.Block(c.User.Id, c.GetInt64("id"))
	c.Session.SetFlash("ok", "Пользователь заблокирован. Вы больше не увидите друг друга.")
	return c.Redirect("/nearby")
}

func (c *PeopleController) ActionReport() error {
	id := c.GetInt64("id")
	block := c.Post("block") == "1"
	if !discovery.Report(c.User.Id, id, c.Post("reason"), block) {
		return c.Redirect("/u/" + strconv.FormatInt(id, 10))
	}
	if block {
		c.Session.SetFlash("ok", "Спасибо, жалоба отправлена модераторам. Пользователь заблокирован.")
		return c.Redirect("/nearby")
	}
	c.Session.SetFlash("ok", "Спасибо, жалоба отправлена модераторам.")
	return c.Redirect("/u/" + strconv.FormatInt(id, 10))
}
