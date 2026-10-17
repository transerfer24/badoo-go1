package control

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"badoo/components/photos"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

// Access — открыт ли админу раздел.
func Access(uid int64, section string) bool {
	if uid <= 0 || section == "" {
		return false
	}
	return app.Db.CreateCommand(`
		SELECT r.id FROM {{control_rules}} r
		INNER JOIN {{control_actions}} a ON a.id = r.action_id AND a.status = 1
		WHERE r.uid = :uid AND a.action = :action AND r.level >= 1 AND r.status = 1
	`).BindValue(":uid", uid).BindValue(":action", section).MustInt() > 0
}

// IsSuper — суперадмин: его права из админки не меняются, новые разделы открываются ему сами.
func IsSuper(uid int64) bool {
	return app.Db.CreateCommand("SELECT id FROM {{control_superadmins}} WHERE uid = :uid").BindValue(":uid", uid).MustInt() > 0
}

// IsAdmin — есть хоть один раздел админки (для ссылки «Управление» на сайте и защиты от блокировки).
func IsAdmin(uid int64) bool {
	return uid > 0 && app.Db.CreateCommand("SELECT COUNT(*) FROM {{control_rules}} WHERE uid = :uid AND status = 1").BindValue(":uid", uid).MustInt() > 0
}

// MakeSuper — сделать суперадмином (команда control/super): все разделы сразу. who — email или id.
func MakeSuper(who string) (db.Row, error) {
	who = strings.ToLower(strings.TrimSpace(who))
	id, _ := strconv.ParseInt(who, 10, 64)
	user := app.Db.CreateCommand("SELECT id, name, email FROM {{users}} WHERE email = :who OR id = :id").BindValue(":who", who).BindValue(":id", id).MustRow()
	if user == nil {
		return nil, fmt.Errorf("пользователь %q не найден — сначала войдите на сайт через Neuronet", who)
	}
	uid := user.Int64("id")
	err := app.Db.Transaction(func(tx *db.Tx) error {
		if tx.CreateCommand("SELECT COUNT(*) FROM {{control_superadmins}} WHERE uid = :uid", db.P{":uid": uid}).MustInt() == 0 {
			tx.MustInsert("{{control_superadmins}}", db.H{"uid": uid})
		}
		tx.CreateCommand(`INSERT INTO {{control_rules}} (uid, action_id, level, status)
			SELECT :uid, a.id, 1, 1 FROM {{control_actions}} a
			WHERE NOT EXISTS (SELECT 1 FROM {{control_rules}} r WHERE r.uid = :uid AND r.action_id = a.id)`, db.P{":uid": uid}).MustExecute()
		tx.MustUpdate("{{control_rules}}", db.H{"status": 1}, db.H{"uid": uid})
		return nil
	})
	return user, err
}

// Menu — разделы админа для лейаута: без группы — пунктами, с группой — выпадающим списком.
func Menu(uid int64) []web.M {
	rows := app.Db.CreateCommand(`
		SELECT a.id, a.action, a.name, a.cid, c.name AS cat_name
		FROM {{control_actions}} a
		INNER JOIN {{control_rules}} r ON r.action_id = a.id AND r.uid = :uid AND r.status = 1
		LEFT JOIN {{control_categories}} c ON c.id = a.cid
		WHERE a.status = 1
		ORDER BY a.cid ASC, a.priority DESC, a.id ASC
	`).BindValue(":uid", uid).MustAll()

	badges := Badges()
	menu := []web.M{}
	groups := map[int64]int{}
	for _, r := range rows {
		url := "/control/" + r.Str("action")
		if r.Str("action") == "index" {
			url = "/control"
		}
		item := web.M{"action": r.Str("action"), "name": r.Str("name"), "url": url, "icon": icons[r.Str("action")], "badge": badges[r.Str("action")]}
		cid := r.Int64("cid")
		if cid == 0 || r.Str("cat_name") == "" {
			menu = append(menu, web.M{"item": item})
			continue
		}
		if i, ok := groups[cid]; ok {
			menu[i]["items"] = append(menu[i]["items"].([]web.M), item)
			menu[i]["badge"] = menu[i]["badge"].(int) + badges[r.Str("action")]
			continue
		}
		groups[cid] = len(menu)
		menu = append(menu, web.M{"group": r.Str("cat_name"), "items": []web.M{item}, "badge": badges[r.Str("action")]})
	}
	return menu
}

// иконки разделов меню (Font Awesome 4); новый раздел без иконки — кружок
var icons = map[string]string{
	"index": "fa-dashboard", "users": "fa-users", "chats": "fa-comments", "meetups": "fa-calendar",
	"reports": "fa-flag", "verification": "fa-check-circle", "photos": "fa-picture-o",
	"log": "fa-history", "permissions": "fa-key",
}

// Badges — счётчики очередей модерации в меню (у пункта и у его группы): новые жалобы, селфи на проверке.
func Badges() map[string]int {
	r := app.Db.CreateCommand(`SELECT
		(SELECT COUNT(*) FROM {{reports}} WHERE status = 0) AS reports,
		(SELECT COUNT(*) FROM {{users}} WHERE verified = 1 AND banned = 0) AS verification`).MustRow()
	return map[string]int{"reports": r.Int("reports"), "verification": r.Int("verification")}
}

// access — фильтры раздела: вошёл (иначе /login), раздел открыт (иначе на первый доступный / на сайт),
// pages — экшены-страницы (GET), всё остальное — только POST (данные для JS страницы, CSRF проверяет ядро).
func access(section string, pages ...string) []web.Behavior {
	return []web.Behavior{
		web.AccessControl{Rules: []web.AccessRule{{Allow: true, Roles: []string{"@"}}}, Deny: func(c *web.Context) error {
			if c.Request.IsPost {
				return c.Json(web.M{"status": 401, "errors": web.M{"modal_message": "Войдите заново"}})
			}
			return c.Redirect("/login")
		}},
		sectionFilter{section: section, pages: pages},
		web.RateLimit{Limit: 600, Window: time.Minute},
	}
}

type sectionFilter struct {
	section string
	pages   []string
}

func (f sectionFilter) BeforeAction(c *web.Context, action string) error {
	isPage := slices.Contains(f.pages, action)
	if !isPage && !c.Request.IsPost {
		return web.ErrMethodNotAllowed()
	}
	if Access(c.User.Id, f.section) {
		return nil
	}
	if !isPage {
		return c.Json(web.M{"status": 403, "errors": web.M{"modal_message": "Нет доступа к разделу"}})
	}
	// нет этого раздела — на первый открытый
	first := app.Db.CreateCommand(`
		SELECT a.action FROM {{control_rules}} r
		INNER JOIN {{control_actions}} a ON a.id = r.action_id AND a.status = 1
		WHERE r.uid = :uid AND r.status = 1
		ORDER BY a.priority DESC, a.id LIMIT 1
	`).BindValue(":uid", c.User.Id).MustString()
	if first != "" && first != f.section {
		if first == "index" {
			return c.Redirect("/control")
		}
		return c.Redirect("/control/" + first)
	}
	return c.Redirect("/")
}

// logAction — запись в журнал действий (control_log): кто, над кем, что сделал.
func logAction(c *web.Context, uid int64, action, details string) {
	if len([]rune(details)) > 500 {
		details = string([]rune(details)[:500])
	}
	app.Db.MustInsert("{{control_log}}", db.H{
		"admin_id": c.User.Id, "user_id": uid, "action": action, "details": details,
		"ip": c.Request.UserIP, "created_at": time.Now().UTC(),
	})
	app.Log.Info("control: "+action, "uid", uid, "admin", c.User.Id, "details", details)
}

/*
paginate — Paginate ядра для списков со скролл-подгрузкой.

Paginate ядра на странице дальше последней отдаёт последнюю страницу ещё раз. Скролл-подгрузка ждёт пустой ответ,
чтобы остановиться, — поэтому за последней страницей здесь пустой список.
*/
func paginate(q *db.Query, page, perPage int) (*db.Page, error) {
	p, err := q.Paginate(page, perPage)
	if err == nil && page > p.Page {
		p.Items = db.Rows{}
		p.Page = page
		p.HasNext = false
	}
	return p, err
}

func mustPaginate(q *db.Query, page, perPage int) *db.Page {
	p, err := paginate(q, page, perPage)
	if err != nil {
		panic(err)
	}
	return p
}

/*
prepare — строки выборки → JSON для JS админки (array_map перед asJson):

  - даты (DATETIME в MySQL, текст в SQLite тестов) → unix-секунды: в JS moment.unix(v), 0 — пусто;
  - ключи файлов (photos.file, verify_file) → адреса (presigned для закрытого S3).

Колонки дат: *_at, last_seen, date; файлы: photo, file, verify_file, host_photo, a_photo, b_photo.
*/
func prepare(rows db.Rows) db.Rows {
	for _, r := range rows {
		prepareRow(r)
	}
	return rows
}

func prepareRow(r db.Row) db.Row {
	if r == nil {
		return nil
	}
	for k := range r {
		switch {
		case strings.HasSuffix(k, "_at") || strings.HasSuffix(k, "last_seen") || k == "date" || k == "premium_until":
			if t := r.Time(k); !t.IsZero() {
				r[k] = t.Unix()
			} else {
				r[k] = 0
			}
		case k == "photo" || k == "file" || k == "verify_file" || strings.HasSuffix(k, "_photo"):
			r[k] = photos.Url(r.Str(k))
		}
	}
	return r
}

// since — время «N назад» для фильтров вида period=24h.
func since(period string) time.Time {
	d := map[string]time.Duration{"5m": 5 * time.Minute, "1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}[period]
	if d == 0 {
		return time.Time{}
	}
	return time.Now().Add(-d).UTC()
}

// parseDay — YYYY-MM-DD из фильтра (UTC); ok=false — пусто или мусор.
func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	return t, err == nil
}
