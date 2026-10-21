package controllers

import (
	"badoo/components/discovery"
	"badoo/components/meetups"
	"badoo/components/photos"
	"badoo/components/users"

	"whales.team/go1/web"
)

/*
ApiPremiumController и ApiMetaController (перенос из api_v1.go):

	GET    /api/v1/premium          {plans, active, until, testMode}
	POST   /api/v1/premium {plan}   MeDTO — в dev-режиме без оплаты, иначе 501 payments
	DELETE /api/v1/premium          MeDTO
	GET    /api/v1/meta             справочники для экранов: города, интересы, цели, причины жалоб, палитры
*/
type ApiPremiumController struct{ ApiController }

func (c *ApiPremiumController) ActionIndex() error {
	me := users.Current(c.Context)
	return c.Json(web.M{"plans": users.Plans, "active": me.IsPremium(), "until": users.Ts(me.PremiumUntil), "testMode": users.DevMode()})
}

func (c *ApiPremiumController) ActionActivate() error {
	if !users.DevMode() {
		return apiFail(c.Context, 501, "payments", "Оплата ещё не подключена.")
	}
	var in struct {
		Plan string `json:"plan"`
	}
	if !apiBind(c.Context, &in) {
		return nil
	}
	if !users.ActivatePremium(users.Current(c.Context), in.Plan) {
		return apiFail(c.Context, 400, "plan", "Выберите тариф.")
	}
	return writeMe(c.Context, c.User.Id)
}

func (c *ApiPremiumController) ActionCancel() error {
	users.CancelPremium(c.User.Id)
	return writeMe(c.Context, c.User.Id)
}

type ApiMetaController struct{ ApiController }

func (c *ApiMetaController) Init() { c.Public = []string{"index"} }

func (c *ApiMetaController) ActionIndex() error {
	type kv struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	intents := []kv{}
	for _, k := range []string{"serious", "casual", "friends", "chat", "unsure"} {
		intents = append(intents, kv{k, users.IntentLabels[k]})
	}
	type city struct {
		Name string  `json:"name"`
		Lat  float64 `json:"lat"`
		Lng  float64 `json:"lng"`
	}
	cities := []city{}
	for _, x := range users.Cities {
		cities = append(cities, city{x.Name, x.Lat, x.Lng})
	}
	return c.Json(web.M{
		"cities": cities, "interests": users.InterestOptions, "intents": intents, "reportReasons": discovery.ReportReasons,
		"palettes": users.AvatarPalettes, "maxPhotos": photos.MaxPhotos, "dev": users.DevMode(), "meetupKinds": meetups.Kinds,
		"languages": users.Languages(), "langLevels": users.LangLevels, "maxLangs": users.MaxLangs,
	})
}
