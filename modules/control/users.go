package control

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"badoo/components/photos"
	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

// UsersController — пользователи: список с фильтрами, карточка (анкета, фото, активность, пары и переписка,
// жалобы, устройства, встречи, журнал) и действия модератора (блокировка, верификация, Premium, правка, фото, выход).
type UsersController struct{ web.Controller }

func (c *UsersController) Behaviors() []web.Behavior { return access("users", "index", "view") }

func (c *UsersController) Init() { c.Layout = "control" }

func (c *UsersController) ActionIndex() error {
	return c.Render("users", web.M{"section": "users", "cities": users.Cities, "intents": users.IntentLabels})
}

func (c *UsersController) ActionView() error {
	uid := c.GetInt64("uid")
	if app.Db.CreateCommand("SELECT id FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustInt() == 0 {
		return c.Redirect("/control/users")
	}
	return c.Render("view", web.M{"section": "users", "uid": uid, "is_super": IsSuper(c.User.Id), "cities": users.Cities})
}

// Список: скролл-подгрузка, поиск и фильтры (все — необязательные)
func (c *UsersController) ActionGetUsers() error {
	res := web.M{}
	page := max(1, c.PostInt("page", 1))
	now := time.Now().UTC()

	q := app.Db.Query().
		Select("u.id", "u.name", "u.email", "u.gender", "u.birthdate", "u.city", "u.verified", "u.onboarded", "u.banned",
			"u.ban_reason", "u.invisible", "u.premium_until", "u.created_at", "u.last_seen", "u.neuronet_uid",
			"(SELECT p.file FROM {{photos}} p WHERE p.user_id = u.id ORDER BY p.pos, p.id LIMIT 1) AS photo",
			"(SELECT COUNT(*) FROM {{photos}} p WHERE p.user_id = u.id) AS photos_count",
			"(SELECT COUNT(*) FROM {{reports}} r WHERE r.reported_id = u.id) AS reports_count",
			"(SELECT COUNT(*) FROM {{votes}} v WHERE v.target_id = u.id AND v.liked = 1) AS likes_count",
			"(SELECT COUNT(*) FROM {{matches}} x WHERE x.user_a = u.id OR x.user_b = u.id) AS matches_count").
		From("{{users}} u")

	if s := strings.TrimSpace(c.Post("search")); s != "" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
		q.AndWhere(db.Raw("(u.name LIKE :s OR u.email LIKE :s OR u.id = :sid OR u.neuronet_uid = :sid)", db.P{":s": "%" + s + "%", ":sid": id}))
	}
	switch c.Post("status") {
	case "active":
		q.AndWhere(db.Raw("u.banned = 0"))
	case "banned":
		q.AndWhere(db.Raw("u.banned = 1"))
	}
	switch c.Post("onboarded") {
	case "1":
		q.AndWhere(db.Raw("u.onboarded = 1"))
	case "0":
		q.AndWhere(db.Raw("u.onboarded = 0"))
	}
	if g := c.Post("gender"); g == "m" || g == "f" || g == "x" {
		q.AndWhere(db.H{"u.gender": g})
	}
	if v := c.Post("verified"); v == "0" || v == "1" || v == "2" {
		q.AndWhere(db.H{"u.verified": v})
	}
	switch c.Post("premium") {
	case "1":
		q.AndWhere(db.Raw("u.premium_until > :now", db.P{":now": now}))
	case "0":
		q.AndWhere(db.Raw("(u.premium_until IS NULL OR u.premium_until <= :now)", db.P{":now": now}))
	}
	if city := strings.TrimSpace(c.Post("city")); city != "" {
		q.AndWhere(db.H{"u.city": city})
	}
	if intent := c.Post("intent"); intent != "" {
		q.AndWhere(db.H{"u.intent": intent})
	}
	// возраст: «от» — родился не позже, «до» — не раньше
	if a := c.PostInt("age_from"); a > 0 {
		q.AndWhere(db.Raw("u.birthdate <> '' AND u.birthdate <= :bmax", db.P{":bmax": now.AddDate(-a, 0, 0).Format("2006-01-02")}))
	}
	if a := c.PostInt("age_to"); a > 0 {
		q.AndWhere(db.Raw("u.birthdate >= :bmin", db.P{":bmin": now.AddDate(-(a + 1), 0, 1).Format("2006-01-02")}))
	}
	if t, ok := parseDay(c.Post("created_from")); ok {
		q.AndWhere(db.Raw("u.created_at >= :cfrom", db.P{":cfrom": t}))
	}
	if t, ok := parseDay(c.Post("created_to")); ok {
		q.AndWhere(db.Raw("u.created_at < :cto", db.P{":cto": t.AddDate(0, 0, 1)}))
	}
	if t := since(c.Post("seen")); !t.IsZero() {
		q.AndWhere(db.Raw("u.last_seen >= :seen", db.P{":seen": t}))
	}
	switch c.Post("photos") {
	case "1":
		q.AndWhere(db.Raw("EXISTS (SELECT 1 FROM {{photos}} p WHERE p.user_id = u.id)"))
	case "0":
		q.AndWhere(db.Raw("NOT EXISTS (SELECT 1 FROM {{photos}} p WHERE p.user_id = u.id)"))
	}
	if c.Post("reported") == "1" {
		q.AndWhere(db.Raw("EXISTS (SELECT 1 FROM {{reports}} r WHERE r.reported_id = u.id)"))
	}
	if c.Post("app") == "1" {
		q.AndWhere(db.Raw("EXISTS (SELECT 1 FROM {{sessions}} s WHERE s.user_id = u.id)"))
	}
	if c.Post("invisible") == "1" {
		q.AndWhere(db.Raw("u.invisible = 1"))
	}

	switch c.Post("sort") {
	case "seen":
		q.OrderBy("u.last_seen DESC")
	case "reports":
		q.OrderBy("reports_count DESC", "u.id DESC")
	case "likes":
		q.OrderBy("likes_count DESC", "u.id DESC")
	case "old":
		q.OrderBy("u.id ASC")
	default:
		q.OrderBy("u.id DESC")
	}

	p, err := paginate(q, page, 50)
	if err != nil {
		app.Log.Error("control: список пользователей", "err", err)
		res["status"] = 200
		res["items"] = db.Rows{}
		return c.Json(res)
	}
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Карточка: анкета, фото, счётчики активности, права в админке
func (c *UsersController) ActionGetUserInfo() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	item := app.Db.CreateCommand(`
		SELECT u.id, u.email, u.neuronet_uid, u.name, u.birthdate, u.gender, u.seeking, u.bio, u.city, u.lat, u.lng, u.height,
		       u.intent, u.interests, u.verified, u.verify_file, u.premium_until, u.invisible, u.age_min, u.age_max, u.max_km,
		       u.verified_only, u.onboarded, u.created_at, u.last_seen, u.banned, u.ban_reason, u.banned_at, u.banned_by,
		       b.name AS banned_by_name
		FROM {{users}} u LEFT JOIN {{users}} b ON b.id = u.banned_by
		WHERE u.id = :uid
	`).BindValue(":uid", uid).MustRow()
	if item == nil {
		res["status"] = 404
		return c.Json(res)
	}
	item["age"] = users.AgeOf(item.Str("birthdate"))
	item["intent_label"] = users.IntentLabels[item.Str("intent")]
	item["gender_label"] = users.GenderLabels[item.Str("gender")]

	counts := app.Db.CreateCommand(`SELECT
		(SELECT COUNT(*) FROM {{votes}} WHERE voter_id = :uid AND liked = 1) AS likes_given,
		(SELECT COUNT(*) FROM {{votes}} WHERE voter_id = :uid AND liked = 0) AS passes_given,
		(SELECT COUNT(*) FROM {{votes}} WHERE target_id = :uid AND liked = 1) AS likes_received,
		(SELECT COUNT(*) FROM {{votes}} WHERE target_id = :uid AND liked = 0) AS passes_received,
		(SELECT COUNT(*) FROM {{matches}} WHERE user_a = :uid OR user_b = :uid) AS matches,
		(SELECT COUNT(*) FROM {{messages}} WHERE sender_id = :uid) AS messages_sent,
		(SELECT COUNT(*) FROM {{visits}} WHERE visited_id = :uid) AS visits_in,
		(SELECT COUNT(*) FROM {{visits}} WHERE visitor_id = :uid) AS visits_out,
		(SELECT COUNT(*) FROM {{reports}} WHERE reported_id = :uid) AS reports_in,
		(SELECT COUNT(*) FROM {{reports}} WHERE reporter_id = :uid) AS reports_out,
		(SELECT COUNT(*) FROM {{blocks}} WHERE blocked_id = :uid) AS blocked_by,
		(SELECT COUNT(*) FROM {{blocks}} WHERE blocker_id = :uid) AS blocks,
		(SELECT COUNT(*) FROM {{sessions}} WHERE user_id = :uid) AS sessions,
		(SELECT COUNT(*) FROM {{push_tokens}} WHERE user_id = :uid) AS push_tokens,
		(SELECT COUNT(*) FROM {{meetups}} WHERE host_id = :uid) AS meetups_hosted,
		(SELECT COUNT(*) FROM {{meetup_going}} WHERE user_id = :uid) AS meetups_going
	`).BindValue(":uid", uid).MustRow()

	res["status"] = 200
	res["item"] = prepareRow(item)
	res["photos"] = prepare(app.Db.CreateCommand("SELECT id, file, pos FROM {{photos}} WHERE user_id = :uid ORDER BY pos, id").BindValue(":uid", uid).MustAll())
	res["counts"] = counts
	res["is_admin"] = IsAdmin(uid)
	res["is_super"] = IsSuper(uid)
	return c.Json(res)
}

// banGuard — нельзя тронуть себя, суперадмина, а админа — только суперадмину.
func (c *UsersController) banGuard(uid int64) string {
	switch {
	case uid == c.User.Id:
		return "Нельзя сделать это с самим собой"
	case IsSuper(uid):
		return "Суперадмина заблокировать нельзя"
	case IsAdmin(uid) && !IsSuper(c.User.Id):
		return "Админа может заблокировать только суперадмин — сначала снимите ему права"
	}
	return ""
}

// Блокировка / разблокировка: banned = 1 — не войдёт, пропадёт из выдачи, выйдет на всех устройствах
func (c *UsersController) ActionSetBan() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	ban := c.PostInt("ban") == 1
	reason := strings.TrimSpace(c.Post("reason"))

	if app.Db.CreateCommand("SELECT id FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustInt() == 0 {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Пользователь не найден"}
		return c.Json(res)
	}
	if ban {
		if msg := c.banGuard(uid); msg != "" {
			res["status"] = 500
			res["errors"] = web.M{"modal_message": msg}
			return c.Json(res)
		}
		if utf8.RuneCountInString(reason) > 255 {
			res["status"] = 500
			res["errors"] = web.M{"reason": "Причина — до 255 символов"}
			return c.Json(res)
		}
		users.Ban(uid, c.User.Id, reason)
		// жалобы на него — разобраны
		app.Db.MustUpdate("{{reports}}", db.H{"status": 1, "resolved_by": c.User.Id, "resolved_at": time.Now().UTC()}, db.H{"reported_id": uid, "status": 0})
		logAction(c.Context, uid, "ban", reason)
	} else {
		users.Unban(uid)
		logAction(c.Context, uid, "unban", "")
	}
	res["status"] = 200
	return c.Json(res)
}

// Верификация селфи: 2 — подтвердить, 0 — отклонить (селфи удаляется)
func (c *UsersController) ActionSetVerified() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	v := c.PostInt("verified")
	if v != 0 && v != 2 {
		res["status"] = 500
		res["errors"] = web.M{"modal_message": "Неверный статус"}
		return c.Json(res)
	}
	users.Verify(uid, v == 2)
	if v == 2 {
		logAction(c.Context, uid, "verify", "")
	} else {
		logAction(c.Context, uid, "unverify", strings.TrimSpace(c.Post("reason")))
	}
	res["status"] = 200
	return c.Json(res)
}

// Premium: days > 0 — продлить, 0 — отключить
func (c *UsersController) ActionSetPremium() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	days := c.PostInt("days")
	if days < 0 || days > 3650 {
		res["status"] = 500
		res["errors"] = web.M{"days": "От 0 до 3650 дней"}
		return c.Json(res)
	}
	users.GrantPremium(uid, days)
	if days > 0 {
		logAction(c.Context, uid, "premium", "+"+strconv.Itoa(days)+" дн.")
	} else {
		logAction(c.Context, uid, "premium-off", "")
	}
	res["status"] = 200
	return c.Json(res)
}

// Правка анкеты модератором: имя, о себе, город, видимость (невидимку можно выключить)
func (c *UsersController) ActionEditUserInfo() error {
	res := web.M{}
	errs := web.M{}
	uid := c.PostInt64("uid")
	name := strings.TrimSpace(c.Post("name"))
	bio := strings.TrimSpace(c.Post("bio"))
	city := strings.TrimSpace(c.Post("city"))

	if l := utf8.RuneCountInString(name); l < 1 || l > 40 {
		errs["name"] = "Имя — от 1 до 40 символов"
	}
	if utf8.RuneCountInString(bio) > 500 {
		errs["bio"] = "О себе — до 500 символов"
	}
	h := db.H{"name": name, "bio": bio}
	if city != "" {
		ct, ok := users.CityByName(city)
		if !ok {
			errs["city"] = "Город не из списка"
		} else {
			h["city"], h["lat"], h["lng"] = ct.Name, ct.Lat, ct.Lng
		}
	}
	if c.Post("invisible") == "0" {
		h["invisible"] = false
	}
	if len(errs) > 0 {
		res["status"] = 500
		res["errors"] = errs
		return c.Json(res)
	}
	app.Db.MustUpdate("{{users}}", h, db.H{"id": uid})
	logAction(c.Context, uid, "edit", "имя: "+name)
	res["status"] = 200
	return c.Json(res)
}

// Удалить фото анкеты (модерация)
func (c *UsersController) ActionDeletePhoto() error {
	res := web.M{}
	ph := app.Db.CreateCommand("SELECT id, user_id, file FROM {{photos}} WHERE id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if ph == nil {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Фото не найдено"}
		return c.Json(res)
	}
	photos.Delete(ph.Str("file"))
	app.Db.MustDelete("{{photos}}", db.H{"id": ph.Int64("id")})
	logAction(c.Context, ph.Int64("user_id"), "photo-delete", "фото #"+ph.Str("id"))
	res["status"] = 200
	return c.Json(res)
}

// Сделать фото главным
func (c *UsersController) ActionMainPhoto() error {
	res := web.M{}
	ph := app.Db.CreateCommand("SELECT id, user_id FROM {{photos}} WHERE id = :id").BindValue(":id", c.PostInt64("id")).MustRow()
	if ph == nil || !photos.SetMain(ph.Int64("user_id"), ph.Int64("id")) {
		res["status"] = 404
		res["errors"] = web.M{"modal_message": "Фото не найдено"}
		return c.Json(res)
	}
	res["status"] = 200
	return c.Json(res)
}

// Выйти на всех устройствах (без блокировки)
func (c *UsersController) ActionLogout() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	if uid != c.User.Id && IsSuper(uid) && !IsSuper(c.User.Id) {
		res["status"] = 500
		res["errors"] = web.M{"modal_message": "Суперадмина может разлогинить только суперадмин"}
		return c.Json(res)
	}
	n := users.Logout(uid)
	logAction(c.Context, uid, "logout", strconv.FormatInt(n, 10)+" сессий приложения")
	res["status"] = 200
	return c.Json(res)
}

// Удалить аккаунт со всеми данными (только суперадмин)
func (c *UsersController) ActionDeleteUser() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	if !IsSuper(c.User.Id) {
		res["status"] = 403
		res["errors"] = web.M{"modal_message": "Удалять аккаунты может только суперадмин"}
		return c.Json(res)
	}
	if msg := c.banGuard(uid); msg != "" || IsAdmin(uid) {
		if msg == "" {
			msg = "Сначала снимите права в админке"
		}
		res["status"] = 500
		res["errors"] = web.M{"modal_message": msg}
		return c.Json(res)
	}
	email := app.Db.CreateCommand("SELECT email FROM {{users}} WHERE id = :uid").BindValue(":uid", uid).MustString()
	if err := users.Delete(uid); err != nil {
		app.Log.Error("control: удаление аккаунта", "uid", uid, "err", err)
		res["status"] = 500
		res["errors"] = web.M{"modal_message": "Не удалось удалить"}
		return c.Json(res)
	}
	logAction(c.Context, uid, "delete", email)
	res["status"] = 200
	return c.Json(res)
}

// ── Вкладки карточки ───────────────────────────────────────────────────────

// Пары и переписка: собеседник, последнее сообщение, сколько сообщений
func (c *UsersController) ActionGetMatches() error {
	res := web.M{}
	p := mustPaginate(app.Db.Query().
		Select("x.id AS match_id", "x.created_at", "x.last_at", "u.id", "u.name", "u.banned", "u.verified",
			"(SELECT p.file FROM {{photos}} p WHERE p.user_id = u.id ORDER BY p.pos, p.id LIMIT 1) AS photo",
			"(SELECT COUNT(*) FROM {{messages}} m WHERE m.match_id = x.id) AS messages",
			"COALESCE(lm.body, '') AS last_body", "COALESCE(lm.sender_id, 0) AS last_sender").
		From("{{matches}} x").
		InnerJoin("{{users}} u", db.Raw("u.id = CASE WHEN x.user_a = :uid THEN x.user_b ELSE x.user_a END", db.P{":uid": c.PostInt64("uid")})).
		LeftJoin("{{messages}} lm", db.Raw("lm.id = x.last_msg_id")).
		Where(db.Raw("(x.user_a = :uid OR x.user_b = :uid)", db.P{":uid": c.PostInt64("uid")})).
		OrderBy("x.last_at DESC", "x.id DESC"), max(1, c.PostInt("page", 1)), 50)
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Голоса: dir = given (он) / received (его), liked = 1 / 0
func (c *UsersController) ActionGetVotes() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	me, other := "v.voter_id", "v.target_id"
	if c.Post("dir") == "received" {
		me, other = "v.target_id", "v.voter_id"
	}
	p := mustPaginate(app.Db.Query().
		Select("v.liked", "v.created_at", "u.id", "u.name", "u.banned",
			"(SELECT p.file FROM {{photos}} p WHERE p.user_id = u.id ORDER BY p.pos, p.id LIMIT 1) AS photo").
		From("{{votes}} v").
		InnerJoin("{{users}} u", db.Raw("u.id = "+other)).
		Where(db.Raw(me+" = :uid AND v.liked = :liked", db.P{":uid": uid, ":liked": c.PostInt("liked", 1) == 1})).
		OrderBy("v.created_at DESC"), max(1, c.PostInt("page", 1)), 50)
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Гости: кто смотрел его анкету (dir = in) / чьи смотрел он (out)
func (c *UsersController) ActionGetVisits() error {
	res := web.M{}
	me, other := "v.visited_id", "v.visitor_id"
	if c.Post("dir") == "out" {
		me, other = "v.visitor_id", "v.visited_id"
	}
	p := mustPaginate(app.Db.Query().
		Select("v.at AS seen_at", "u.id", "u.name", "u.banned",
			"(SELECT p.file FROM {{photos}} p WHERE p.user_id = u.id ORDER BY p.pos, p.id LIMIT 1) AS photo").
		From("{{visits}} v").
		InnerJoin("{{users}} u", db.Raw("u.id = "+other)).
		Where(db.Raw(me+" = :uid", db.P{":uid": c.PostInt64("uid")})).
		OrderBy("v.at DESC"), max(1, c.PostInt("page", 1)), 50)
	res["status"] = 200
	res["items"] = prepare(p.Items)
	res["count"] = p.Total
	return c.Json(res)
}

// Жалобы на него и от него, блокировки (кто его заблокировал и кого он)
func (c *UsersController) ActionGetReports() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	res["status"] = 200
	res["against"] = prepare(app.Db.CreateCommand(`
		SELECT r.id, r.reason, r.status, r.created_at, r.resolved_at, u.id AS uid, u.name
		FROM {{reports}} r LEFT JOIN {{users}} u ON u.id = r.reporter_id
		WHERE r.reported_id = :uid ORDER BY r.id DESC LIMIT 200`).BindValue(":uid", uid).MustAll())
	res["from"] = prepare(app.Db.CreateCommand(`
		SELECT r.id, r.reason, r.status, r.created_at, r.resolved_at, u.id AS uid, u.name, u.banned
		FROM {{reports}} r LEFT JOIN {{users}} u ON u.id = r.reported_id
		WHERE r.reporter_id = :uid ORDER BY r.id DESC LIMIT 200`).BindValue(":uid", uid).MustAll())
	res["blocked_by"] = app.Db.CreateCommand(`
		SELECT u.id, u.name FROM {{blocks}} b INNER JOIN {{users}} u ON u.id = b.blocker_id
		WHERE b.blocked_id = :uid LIMIT 200`).BindValue(":uid", uid).MustAll()
	res["blocks"] = app.Db.CreateCommand(`
		SELECT u.id, u.name FROM {{blocks}} b INNER JOIN {{users}} u ON u.id = b.blocked_id
		WHERE b.blocker_id = :uid LIMIT 200`).BindValue(":uid", uid).MustAll()
	return c.Json(res)
}

// Устройства: сессии приложения (Bearer) и push-токены. Токены целиком не показываем.
func (c *UsersController) ActionGetDevices() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	sessions := app.Db.CreateCommand(`SELECT SUBSTR(token, 1, 6) AS token, platform, created_at, expires_at
		FROM {{sessions}} WHERE user_id = :uid ORDER BY created_at DESC`).BindValue(":uid", uid).MustAll()
	push := app.Db.CreateCommand(`SELECT SUBSTR(token, 1, 24) AS token, platform, created_at
		FROM {{push_tokens}} WHERE user_id = :uid ORDER BY created_at DESC`).BindValue(":uid", uid).MustAll()
	res["status"] = 200
	res["sessions"] = prepare(sessions)
	res["push"] = prepare(push)
	return c.Json(res)
}

// Встречи: организует и идёт
func (c *UsersController) ActionGetMeetups() error {
	res := web.M{}
	uid := c.PostInt64("uid")
	res["status"] = 200
	res["hosted"] = prepare(app.Db.CreateCommand(`
		SELECT m.id, m.title, m.kind, m.place, m.city, m.starts_at, m.ends_at,
		       (SELECT COUNT(*) FROM {{meetup_going}} g WHERE g.meetup_id = m.id) AS going
		FROM {{meetups}} m WHERE m.host_id = :uid ORDER BY m.starts_at DESC LIMIT 100`).BindValue(":uid", uid).MustAll())
	res["going"] = prepare(app.Db.CreateCommand(`
		SELECT m.id, m.title, m.kind, m.place, m.city, m.starts_at, m.ends_at, g.checked_in_at, g.created_at
		FROM {{meetup_going}} g INNER JOIN {{meetups}} m ON m.id = g.meetup_id
		WHERE g.user_id = :uid ORDER BY m.starts_at DESC LIMIT 100`).BindValue(":uid", uid).MustAll())
	return c.Json(res)
}

// Журнал действий админов над этим пользователем
func (c *UsersController) ActionGetLog() error {
	res := web.M{}
	res["status"] = 200
	res["items"] = prepare(app.Db.CreateCommand(`
		SELECT l.id, l.action, l.details, l.created_at, l.admin_id, a.name AS admin_name
		FROM {{control_log}} l LEFT JOIN {{users}} a ON a.id = l.admin_id
		WHERE l.user_id = :uid ORDER BY l.id DESC LIMIT 200`).BindValue(":uid", c.PostInt64("uid")).MustAll())
	return c.Json(res)
}

// Поиск для select2 (добавить админа, фильтр переписки)
func (c *UsersController) ActionSearchUser() error {
	return c.Json(web.M{"items": searchUsers(c.Post("q"))})
}

func searchUsers(q string) db.Rows {
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) < 1 {
		return db.Rows{}
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(q, "#"), 10, 64)
	rows := app.Db.CreateCommand(`
		SELECT id, name, email FROM {{users}}
		WHERE name LIKE :q OR email LIKE :q OR id = :id
		ORDER BY id DESC LIMIT 20
	`).BindValue(":q", "%"+q+"%").BindValue(":id", id).MustAll()
	for _, r := range rows {
		r["text"] = "#" + r.Str("id") + " " + r.Str("name") + " — " + r.Str("email")
	}
	return rows
}
