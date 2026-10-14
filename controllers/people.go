package controllers

import (
	"sort"
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
	people := discovery.Candidates(me, f)
	sort.Slice(people, func(i, j int) bool { return people[i].DistKm < people[j].DistKm })
	if len(people) > 90 {
		people = people[:90]
	}
	users.FillPhotos(people)
	c.View.Params = web.M{"active": "nearby"}

	return c.Render("nearby", web.M{"people": people, "f": f})
}

func (c *PeopleController) ActionProfile() error {
	me := users.Current(c.Context)
	id := c.GetInt64("id")
	u := users.ById(id)
	if u == nil || !u.Onboarded || discovery.BlockedEither(me.Id, id) {
		return c.NotFound()
	}
	u.DistKm = users.HaversineKm(me.Lat, me.Lng, u.Lat, u.Lng)

	res := web.M{"u": u, "match_id": int64(0), "my_vote": -1, "liked_me": false, "reasons": discovery.ReportReasons, "is_me": u.Id == me.Id}
	if u.Id != me.Id {
		discovery.Visit(me, id)
		res["match_id"] = discovery.MatchId(me.Id, id)
		if v := c.Db().CreateCommand("SELECT liked FROM {{votes}} WHERE voter_id = :me AND target_id = :id").BindValue(":me", me.Id).BindValue(":id", id).MustRow(); v != nil {
			res["my_vote"] = v.Int("liked")
		}
		n := c.Db().CreateCommand("SELECT COUNT(*) FROM {{votes}} WHERE voter_id = :id AND target_id = :me AND liked = 1").BindValue(":id", id).BindValue(":me", me.Id).MustInt()
		res["liked_me"] = n > 0 && me.IsPremium()
	}
	c.View.SetTitle(u.Name)
	return c.Render("profile", res)
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
