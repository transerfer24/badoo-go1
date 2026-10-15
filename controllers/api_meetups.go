package controllers

import (
	"time"

	"badoo/components/meetups"
	"badoo/components/users"

	"whales.team/go1/web"
)

/*
ApiMeetupsController — события «встречи этой недели» в приложении (перенос meetups.go):

	GET    /api/v1/meetups?days=7        {meetups: [DTO], kinds}
	POST   /api/v1/meetups  Input        {meetup} — своё событие, организатор сразу идёт
	GET    /api/v1/meetups/<id>          {meetup, people} — список идущих видят те, кто сам идёт
	DELETE /api/v1/meetups/<id>          {ok} — только организатор
	POST   /api/v1/meetups/<id>/join     {meetup}   «Пойду»
	DELETE /api/v1/meetups/<id>/join     {meetup}   «Не пойду»
	POST   /api/v1/meetups/<id>/checkin  {meetup}   «Я на месте»
*/
type ApiMeetupsController struct{ ApiController }

func (c *ApiMeetupsController) ActionIndex() error {
	me := users.Current(c.Context)
	now := time.Now().UTC()
	days := c.GetInt("days")
	if days == 0 {
		days = 7
	}
	return c.Json(web.M{"meetups": meetups.DTOs(me, meetups.Nearby(me, days, now), now), "kinds": meetups.Kinds})
}

func (c *ApiMeetupsController) load() *meetups.Meetup {
	m := meetups.ById(c.GetInt64("id"))
	if m == nil {
		_ = apiFail(c.Context, 404, "not_found", "Событие не найдено или уже удалено.")
	}
	return m
}

func (c *ApiMeetupsController) ActionView() error {
	m := c.load()
	if m == nil {
		return nil
	}
	me := users.Current(c.Context)
	now := time.Now().UTC()
	d := meetups.One(me, m, now)
	type attendee struct {
		UserDTO
		CheckedIn bool  `json:"checkedIn"`
		MatchId   int64 `json:"matchId"`
		MyVote    int   `json:"myVote"`
	}
	people := []attendee{}
	if d.IGo {
		for _, r := range meetups.Attendees(me, m) {
			myVote := -1
			if !r.IsNull("my_vote") {
				myVote = r.Int("my_vote")
			}
			people = append(people, attendee{toDTO(r), r.Bool("checked_in"), r.Int64("match_id"), myVote})
		}
	}
	return c.Json(web.M{"meetup": d, "people": people})
}

func (c *ApiMeetupsController) ActionCreate() error {
	var in meetups.Input
	if !apiBind(c.Context, &in) {
		return nil
	}
	me := users.Current(c.Context)
	now := time.Now().UTC()
	m, field, msg := meetups.Create(me, in, now)
	if field != "" {
		return apiFail(c.Context, 400, field, msg)
	}
	return c.Json(web.M{"meetup": meetups.One(me, m, now)})
}

func (c *ApiMeetupsController) ActionDelete() error {
	if !meetups.Delete(users.Current(c.Context), c.GetInt64("id")) {
		return apiFail(c.Context, 403, "forbidden", "Отменить событие может только организатор.")
	}
	return c.Json(web.M{"ok": true})
}

func (c *ApiMeetupsController) ActionJoin() error    { return c.change(meetups.Join) }
func (c *ApiMeetupsController) ActionLeave() error   { return c.change(meetups.Leave) }
func (c *ApiMeetupsController) ActionCheckin() error { return c.change(meetups.Checkin) }

func (c *ApiMeetupsController) change(fn func(*users.User, *meetups.Meetup, time.Time) (string, string)) error {
	m := c.load()
	if m == nil {
		return nil
	}
	me := users.Current(c.Context)
	now := time.Now().UTC()
	if code, msg := fn(me, m, now); code != "" {
		return apiFail(c.Context, 400, code, msg)
	}
	return c.Json(web.M{"meetup": meetups.One(me, m, now)})
}
