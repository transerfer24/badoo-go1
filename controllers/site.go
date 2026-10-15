package controllers

import (
	"fmt"
	"html"
	"time"
	"unicode"

	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/web"
)

// SiteController — лендинг и страница ошибок.
type SiteController struct{ web.Controller }

func (c *SiteController) Behaviors() []web.Behavior {
	return []web.Behavior{web.RateLimit{Limit: 120, Window: time.Minute}}
}

func (c *SiteController) ActionIndex() error {
	if !c.User.IsGuest {
		return c.Redirect("/encounters")
	}
	count := app.Db.CreateCommand("SELECT COUNT(*) FROM {{users}} WHERE onboarded = 1").MustInt()

	return c.Render("landing", web.M{"count": count})
}

func (c *SiteController) ActionError() error {
	return c.Render("error")
}

// GET /avatar/<id>.svg — сгенерированный аватар для анкет без фото (перенос handleAvatar из images.go).
func (c *SiteController) ActionAvatar() error {
	id := c.GetInt64("id")
	name := app.Db.CreateCommand("SELECT name FROM {{users}} WHERE id = :id").BindValue(":id", id).MustString()
	sum := users.Hash32(id)
	p := users.AvatarPalettes[users.ColorIndex(id)]
	initial := "?"
	for _, r := range name {
		initial = string(unicode.ToUpper(r))
		break
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 400" width="300" height="400">
<defs><linearGradient id="g" gradientTransform="rotate(%d .5 .5)"><stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient></defs>
<rect width="300" height="400" fill="url(#g)"/>
<circle cx="%d%%" cy="%d%%" r="120" fill="#fff" fill-opacity=".12"/>
<circle cx="150" cy="165" r="62" fill="#fff" fill-opacity=".28"/>
<path d="M40 400c8-80 54-130 110-130s102 50 110 130z" fill="#fff" fill-opacity=".28"/>
<text x="150" y="188" text-anchor="middle" font-family="system-ui,sans-serif" font-size="64" font-weight="700" fill="#fff">%s</text>
</svg>`, sum%360, p[0], p[1], 20+sum%60, 15+(sum>>8)%50, html.EscapeString(initial))
	c.Response.Header().Set("Cache-Control", "public, max-age=3600")
	return c.Bytes("image/svg+xml", []byte(svg))
}
