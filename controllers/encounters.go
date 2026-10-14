package controllers

import (
	"badoo/components/discovery"
	"badoo/components/users"

	"whales.team/go1/web"
)

/*
EncountersController — «Встречи»: страница с колодой и её AJAX (перенос handleEncounters/apiEncounters/apiVote/apiUndo).

	GET  /encounters        страница
	GET  /api/encounters    12 карточек под фильтры пользователя
	POST /api/vote          {target, liked} → {match, matchId, name, avatar}
	POST /api/undo          вернуть последнее «нет» (Premium)
*/
type EncountersController struct{ AppController }

func (c *EncountersController) ActionIndex() error {
	c.View.Params = web.M{"active": "encounters"}
	return c.Render("index", web.M{"me": users.Current(c.Context)})
}

func (c *EncountersController) ActionCards() error {
	me := users.Current(c.Context)
	f := discovery.DefaultFilter(me)
	f.ExcludeVoted = true
	f.Limit = 200 // на экран идут 12 лучших — выбирать их из 200 случайно перемешанных достаточно
	list := discovery.Candidates(me, f)
	discovery.Rank(me, list, f.MaxKm)
	if len(list) > 12 {
		list = list[:12]
	}
	users.FillPhotos(list)
	cards := make([]discovery.Card, 0, len(list))
	for _, u := range list {
		cards = append(cards, discovery.ToCard(u))
	}

	return c.Json(web.M{"cards": cards, "premium": me.IsPremium()})
}

func (c *EncountersController) ActionVote() error {
	res, ok := discovery.Vote(users.Current(c.Context), c.PostInt64("target"), c.PostBool("liked"))
	if !ok {
		return c.NotFound()
	}
	return c.Json(res)
}

// Отмена последнего «нет» — премиум-функция.
func (c *EncountersController) ActionUndo() error {
	me := users.Current(c.Context)
	if !me.IsPremium() {
		c.SetStatus(402)
		return c.Json(web.M{"error": "premium"})
	}
	u := discovery.Undo(me)
	if u == nil {
		return c.Json(web.M{"card": nil})
	}
	return c.Json(web.M{"card": discovery.ToCard(u)})
}
