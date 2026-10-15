package controllers

import (
	"badoo/components/discovery"
	"badoo/components/meetups"
	"badoo/components/users"

	"whales.team/go1/web"
)

/*
ApiPeopleController — знакомства в приложении (перенос части api_v1.go):

	GET  /api/v1/encounters              {cards: [UserDTO], premium}
	POST /api/v1/votes  {target, liked}  {match, matchId?, name?, avatar?}
	POST /api/v1/undo                    {card: UserDTO|null}; 402 без Premium
	GET  /api/v1/nearby?ageMin&ageMax&km&seeking&verified&online   {people, filter}
	GET  /api/v1/users/<id>              {user, matchId, myVote, likedMe, isMe, meetups}
	POST /api/v1/users/<id>/block        {ok}
	POST /api/v1/users/<id>/report {reason, block}   {ok}
*/
type ApiPeopleController struct{ ApiController }

func (c *ApiPeopleController) ActionEncounters() error {
	me := users.Current(c.Context)
	return c.Json(web.M{"cards": toDTOs(discovery.Encounters(me)), "premium": me.IsPremium()})
}

func (c *ApiPeopleController) ActionVotes() error {
	var in struct {
		Target int64 `json:"target"`
		Liked  bool  `json:"liked"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	res, ok := discovery.Vote(users.Current(c.Context), in.Target, in.Liked)
	if !ok {
		return apiFail(c.Context, 404, "not_found", "Анкета недоступна.")
	}
	return c.Json(res)
}

func (c *ApiPeopleController) ActionUndo() error {
	me := users.Current(c.Context)
	if !me.IsPremium() {
		return apiFail(c.Context, 402, "premium", "Вернуть анкету можно с Premium.")
	}
	u := discovery.Undo(me)
	if u == nil {
		return c.Json(web.M{"card": nil})
	}
	return c.Json(web.M{"card": toDTO(u)})
}

func (c *ApiPeopleController) ActionNearby() error {
	me := users.Current(c.Context)
	f := discovery.DefaultFilter(me)
	if v := c.GetInt("ageMin"); v > 0 {
		f.AgeMin = users.Clamp(v, 18, 99)
	}
	if v := c.GetInt("ageMax"); v > 0 {
		f.AgeMax = users.Clamp(v, 18, 99)
	}
	if f.AgeMax < f.AgeMin {
		f.AgeMin, f.AgeMax = f.AgeMax, f.AgeMin
	}
	if v := c.GetInt("km"); v > 0 {
		f.MaxKm = users.Clamp(v, 1, 300)
	}
	if s := c.Get("seeking"); s == "m" || s == "f" || s == "any" {
		f.Seeking = s
	}
	if c.Request.URL.Query().Has("verified") {
		f.VerifiedOnly = c.Get("verified") == "1"
	}
	f.OnlineOnly = c.Get("online") == "1"
	people := discovery.Nearby(me, f)
	return c.Json(web.M{"people": toDTOs(people), "filter": web.M{
		"ageMin": f.AgeMin, "ageMax": f.AgeMax, "km": f.MaxKm, "seeking": f.Seeking, "verified": f.VerifiedOnly, "online": f.OnlineOnly}})
}

func (c *ApiPeopleController) ActionUser() error {
	me := users.Current(c.Context)
	id := c.GetInt64("id")
	u := users.ById(id)
	if u == nil || !u.Onboarded || discovery.BlockedEither(me.Id, id) {
		return apiFail(c.Context, 404, "not_found", "Анкета недоступна.")
	}
	u.DistKm = users.HaversineKm(me.Lat, me.Lng, u.Lat, u.Lng)
	rel := discovery.Relate(me, id)
	return c.Json(web.M{"user": toDTO(u), "matchId": rel.MatchId, "myVote": rel.MyVote, "likedMe": rel.LikedMe, "isMe": u.Id == me.Id,
		"meetups": meetups.OfUser(me.Id, u.Id)})
}

func (c *ApiPeopleController) ActionBlock() error {
	discovery.Block(c.User.Id, c.GetInt64("id"))
	return c.Json(web.M{"ok": true})
}

func (c *ApiPeopleController) ActionReport() error {
	var in struct {
		Reason string `json:"reason"`
		Block  bool   `json:"block"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	if !discovery.Report(c.User.Id, c.GetInt64("id"), in.Reason, in.Block) {
		return apiFail(c.Context, 400, "reason", "Выберите причину.")
	}
	return c.Json(web.M{"ok": true})
}
