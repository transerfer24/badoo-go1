package users

// Операции над своим аккаунтом, общие для веб-кабинета (MeController) и API v1 (ApiMeController,
// ApiPremiumController): настройки, Premium, верификация, удаление. Контроллеры только разбирают
// вход и формируют ответ — логика и SQL здесь, в одном месте.

import (
	"hash/fnv"
	"strconv"
	"time"

	"badoo/components/photos"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

// DevMode — AMURA_DEV=1: верификация сразу, Premium без оплаты.
func DevMode() bool { v, _ := app.Params["amura.dev"].(bool); return v }

func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---------- Настройки ----------

type Settings struct {
	AgeMin, AgeMax, MaxKm int
	Seeking               string
	VerifiedOnly          bool
	Invisible             bool
}

// SaveSettings — фильтры и приватность. Невидимка включается только с Premium (без него молча остаётся выключенной —
// веб показывает подсказку, API отвечает 402 до вызова).
func SaveSettings(me *User, s Settings) {
	amin, amax := Clamp(s.AgeMin, 18, 99), Clamp(s.AgeMax, 18, 99)
	if amax < amin {
		amin, amax = amax, amin
	}
	if s.Seeking != "m" && s.Seeking != "f" && s.Seeking != "any" {
		s.Seeking = me.Seeking
	}
	app.Db.MustUpdate("{{users}}", db.H{
		"age_min": amin, "age_max": amax, "max_km": Clamp(s.MaxKm, 1, 300), "seeking": s.Seeking,
		"verified_only": s.VerifiedOnly, "invisible": s.Invisible && me.IsPremium(),
	}, db.H{"id": me.Id})
}

// ---------- Premium ----------

type Plan struct {
	Id     string `json:"id"`
	Title  string `json:"title"`
	Days   int    `json:"days"`
	Price  string `json:"price"`
	Per    string `json:"per"`
	Accent bool   `json:"accent"`
}

var Plans = []Plan{
	{"week", "1 неделя", 7, "4,99 €", "", false},
	{"month", "1 месяц", 30, "12,99 €", "", true},
	{"quarter", "3 месяца", 90, "29,99 €", "10 € / мес", false},
}

// ActivatePremium — продлить от текущей даты окончания (или от сегодня). false — нет такого тарифа.
// Только в dev-режиме: оплата (Stripe Checkout) ещё не подключена — это проверяет вызывающий.
func ActivatePremium(me *User, planId string) bool {
	days := 0
	for _, p := range Plans {
		if p.Id == planId {
			days = p.Days
		}
	}
	if days == 0 {
		return false
	}
	start := time.Now()
	if me.IsPremium() {
		start = me.PremiumUntil
	}
	app.Db.MustUpdate("{{users}}", db.H{"premium_until": start.AddDate(0, 0, days).UTC()}, db.H{"id": me.Id})
	return true
}

func CancelPremium(uid int64) {
	app.Db.MustUpdate("{{users}}", db.H{"premium_until": nil, "invisible": false}, db.H{"id": uid})
}

// ---------- Верификация ----------

// SetVerified — селфи уже сохранено в закрытую папку (photos.Store … KindPrivate); возвращает статус:
// 2 — подтверждён (dev-режим), 1 — на проверке (в проде — очередь модерации).
func SetVerified(uid int64, selfieFile string) int {
	status := 1
	if DevMode() {
		status = 2
	}
	app.Db.MustUpdate("{{users}}", db.H{"verified": status, "verify_file": selfieFile}, db.H{"id": uid})
	return status
}

// ---------- Удаление ----------

// Delete — аккаунт со всеми данными и файлами. В SQLite-версии связанное удалялось каскадом
// (ON DELETE CASCADE); здесь внешних ключей нет — чистим сами, в транзакции.
func Delete(uid int64) error {
	for _, f := range app.Db.CreateCommand("SELECT file FROM {{photos}} WHERE user_id = :uid").BindValue(":uid", uid).MustStrings() {
		photos.Delete(f)
	}
	if vf := app.Db.CreateCommand("SELECT verify_file FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustString(); vf != "" {
		photos.Delete(vf)
	}
	return app.Db.Transaction(func(tx *db.Tx) error {
		for _, t := range []struct{ table, col string }{
			{"photos", "user_id"}, {"votes", "voter_id"}, {"votes", "target_id"}, {"visits", "visitor_id"}, {"visits", "visited_id"},
			{"blocks", "blocker_id"}, {"blocks", "blocked_id"}, {"reports", "reporter_id"}, {"reports", "reported_id"},
			{"sessions", "user_id"}, {"push_tokens", "user_id"}, {"meetup_going", "user_id"},
		} {
			tx.MustDelete("{{"+t.table+"}}", db.H{t.col: uid})
		}
		tx.CreateCommand("DELETE FROM {{messages}} WHERE match_id IN (SELECT id FROM {{matches}} WHERE user_a = :uid OR user_b = :uid)", db.P{":uid": uid}).MustExecute()
		tx.MustDelete("{{matches}}", db.Or(db.H{"user_a": uid}, db.H{"user_b": uid}))
		tx.MustUpdate("{{meetups}}", db.H{"host_id": nil}, db.H{"host_id": uid})
		tx.MustDelete("{{users}}", db.H{"id": uid})
		return nil
	})
}

// ---------- Аватар без фото ----------

// AvatarPalettes — градиенты сгенерированных аватаров; индекс — ColorIndex(id). Приложение рисует их само
// по /api/v1/meta, веб получает готовый SVG /avatar/<id>.svg.
var AvatarPalettes = [][2]string{
	{"#0E7C74", "#5FD3C4"}, {"#E0603F", "#F7B267"}, {"#3D5A80", "#98C1D9"}, {"#7A5C9E", "#D3B8F0"},
	{"#2F855A", "#9AE6B4"}, {"#B83280", "#F6A6D1"}, {"#C05621", "#FBD38D"}, {"#2B6CB0", "#90CDF4"},
}

func ColorIndex(id int64) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.FormatInt(id, 10)))
	return int(h.Sum32() % uint32(len(AvatarPalettes)))
}

// Hash32 — та же свёртка id, что и у ColorIndex (угол градиента и смещения в SVG-аватаре).
func Hash32(id int64) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.FormatInt(id, 10)))
	return h.Sum32()
}
