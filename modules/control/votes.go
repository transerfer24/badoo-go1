package control

import (
	"strconv"
	"strings"
	"time"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

/*
VotesController — симпатии и антиспам (только чтение, приложение не задевает).

  - «Подозрительные» — кто голосовал за период больше порога: сколько лайков, доля лайков, сколько из них
    взаимны, жалобы на него. Признаки (флаги) считаются здесь же — см. voteFlags. Решение (блокировка) —
    в карточке пользователя, отсюда ничего не меняется.
  - «Лента» — последние голоса: кто, кому, лайк или пропуск, взаимно ли.
*/
type VotesController struct{ web.Controller }

func (c *VotesController) Behaviors() []web.Behavior { return access("votes", "index") }

func (c *VotesController) Init() { c.Layout = "control" }

func (c *VotesController) ActionIndex() error {
	return c.Render("votes", web.M{"section": "votes"})
}

// periodSince — начало периода из фильтра (24h, 7d, 30d), по умолчанию def
func periodSince(period, def string) time.Time {
	if t := since(period); !t.IsZero() {
		return t
	}
	return since(def)
}

// Сводка за период: голоса, доля лайков, взаимность, сколько голосовавших
func (c *VotesController) ActionGetSummary() error {
	p := db.P{":since": periodSince(c.Post("period"), "7d")}
	sum := app.Db.CreateCommand(`
		SELECT COUNT(*) AS votes,
		       COALESCE(SUM(v.liked = 1), 0) AS likes,
		       COALESCE(SUM(CASE WHEN v.liked = 1 AND EXISTS (SELECT 1 FROM {{votes}} w
		           WHERE w.voter_id = v.target_id AND w.target_id = v.voter_id AND w.liked = 1) THEN 1 ELSE 0 END), 0) AS mutual,
		       COUNT(DISTINCT v.voter_id) AS voters,
		       COUNT(DISTINCT v.target_id) AS targets
		FROM {{votes}} v WHERE v.created_at >= :since`, p).MustRow()
	top := app.Db.CreateCommand(`SELECT COUNT(*) AS n FROM {{votes}} WHERE created_at >= :since GROUP BY voter_id ORDER BY n DESC LIMIT 1`, p).MustInt()
	return c.Json(web.M{"status": 200, "summary": sum, "top": top})
}

// пороги признаков (флагов) подозрительной активности
const (
	flagMinVotes   = 50 // «лайкает почти всех» — не меньше стольких голосов
	flagLikeRate   = 90 // … и доля лайков от голосов, %
	flagMinLikes   = 30 // «ни одной взаимности» — не меньше стольких лайков
	flagNewAccount = 3  // «новый аккаунт» — дней с регистрации
)

// voteFlags — признаки, за которые человек попал в подозрительные (для подписи и фильтра «только с признаками»)
func voteFlags(r db.Row) []string {
	flags := []string{}
	if r.Int("total") >= flagMinVotes && r.Int("rate") >= flagLikeRate {
		flags = append(flags, "all")
	}
	if r.Int("likes") >= flagMinLikes && r.Int("mutual") == 0 {
		flags = append(flags, "nomutual")
	}
	if t := r.Time("created_at"); !t.IsZero() && time.Since(t) < flagNewAccount*24*time.Hour {
		flags = append(flags, "new")
	}
	if r.Int("reports") > 0 {
		flags = append(flags, "reports")
	}
	return flags
}

/*
Подозрительные: голоса за период, сгруппированные по голосующему, не меньше min (по умолчанию 30).
Свой SQL с LIMIT/OFFSET: агрегат — в подзапросе, к нему пользователи (так GROUP BY не зависит от их колонок).
*/
func (c *VotesController) ActionGetSuspects() error {
	page, perPage := max(1, c.PostInt("page", 1)), 50
	minVotes := c.PostInt("min", 30)
	if minVotes < 1 {
		minVotes = 1
	}
	p := db.P{":since": periodSince(c.Post("period"), "7d"), ":min": minVotes}

	where := ""
	switch c.Post("flag") {
	case "all":
		where = " AND s.total >= :fmin AND s.likes * 100 >= s.total * :frate"
		p[":fmin"], p[":frate"] = flagMinVotes, flagLikeRate
	case "nomutual":
		where = " AND s.likes >= :flikes AND s.mutual = 0"
		p[":flikes"] = flagMinLikes
	case "new":
		where = " AND u.created_at >= :fnew"
		p[":fnew"] = time.Now().UTC().Add(-flagNewAccount * 24 * time.Hour)
	case "reports":
		where = " AND EXISTS (SELECT 1 FROM {{reports}} r WHERE r.reported_id = u.id)"
	}
	if c.Post("banned") != "1" {
		where += " AND u.banned = 0"
	}

	order := "s.likes DESC, s.total DESC"
	switch c.Post("sort") {
	case "total":
		order = "s.total DESC"
	case "rate":
		order = "(s.likes * 1.0 / s.total) DESC, s.total DESC"
	case "mutual":
		order = "s.mutual ASC, s.likes DESC"
	case "new":
		order = "u.created_at DESC"
	}

	from := `
		FROM (SELECT v.voter_id, COUNT(*) AS total, COALESCE(SUM(v.liked = 1), 0) AS likes,
		             COALESCE(SUM(CASE WHEN v.liked = 1 AND EXISTS (SELECT 1 FROM {{votes}} w
		                 WHERE w.voter_id = v.target_id AND w.target_id = v.voter_id AND w.liked = 1) THEN 1 ELSE 0 END), 0) AS mutual
		      FROM {{votes}} v WHERE v.created_at >= :since
		      GROUP BY v.voter_id HAVING COUNT(*) >= :min) s
		INNER JOIN {{users}} u ON u.id = s.voter_id
		WHERE 1 = 1` + where

	count := app.Db.CreateCommand("SELECT COUNT(*)"+from, p).MustInt()
	items := db.Rows{}
	if int64((page-1)*perPage) < count {
		items = app.Db.CreateCommand(`
			SELECT u.id, u.name, u.email, u.gender, u.birthdate, u.city, u.verified, u.banned, u.premium_until, u.created_at, u.last_seen,
			       s.total, s.likes, s.mutual,
			       (SELECT COUNT(*) FROM {{reports}} r WHERE r.reported_id = u.id) AS reports,
			       (SELECT COUNT(*) FROM {{votes}} t WHERE t.target_id = u.id AND t.liked = 1) AS likes_in,
			       (SELECT ph.file FROM {{photos}} ph WHERE ph.user_id = u.id ORDER BY ph.pos, ph.id LIMIT 1) AS photo`+from+`
			ORDER BY `+order+` LIMIT `+strconv.Itoa(perPage)+` OFFSET `+strconv.Itoa((page-1)*perPage), p).MustAll()
		for _, r := range items {
			r["rate"] = r.Int("likes") * 100 / max(1, r.Int("total")) // доля лайков, % (в Go: деление в MySQL и SQLite разное)
			r["flags"] = voteFlags(r)
		}
	}
	return c.Json(web.M{"status": 200, "items": prepare(items), "count": count})
}

// Лента голосов: свежие сверху; uid — голоса от него и за него, liked — только лайки/пропуски
func (c *VotesController) ActionGetFeed() error {
	q := app.Db.Query().
		Select("v.voter_id", "v.target_id", "v.liked", "v.created_at",
			"a.name AS voter_name", "a.banned AS voter_banned", "b.name AS target_name", "b.banned AS target_banned",
			"(SELECT ph.file FROM {{photos}} ph WHERE ph.user_id = v.voter_id ORDER BY ph.pos, ph.id LIMIT 1) AS voter_photo",
			"(SELECT ph.file FROM {{photos}} ph WHERE ph.user_id = v.target_id ORDER BY ph.pos, ph.id LIMIT 1) AS target_photo",
			"(SELECT w.liked FROM {{votes}} w WHERE w.voter_id = v.target_id AND w.target_id = v.voter_id) AS back").
		From("{{votes}} v").
		LeftJoin("{{users}} a", db.Raw("a.id = v.voter_id")).
		LeftJoin("{{users}} b", db.Raw("b.id = v.target_id"))
	if s := strings.TrimPrefix(strings.TrimSpace(c.Post("uid")), "#"); s != "" {
		uid, _ := strconv.ParseInt(s, 10, 64)
		switch c.Post("dir") {
		case "out":
			q.AndWhere(db.H{"v.voter_id": uid})
		case "in":
			q.AndWhere(db.H{"v.target_id": uid})
		default:
			q.AndWhere(db.Raw("(v.voter_id = :uid OR v.target_id = :uid)", db.P{":uid": uid}))
		}
	}
	switch c.Post("liked") {
	case "1":
		q.AndWhere(db.Raw("v.liked = 1"))
	case "0":
		q.AndWhere(db.Raw("v.liked = 0"))
	}
	if t := since(c.Post("period")); !t.IsZero() {
		q.AndWhere(db.Raw("v.created_at >= :since", db.P{":since": t}))
	}
	q.OrderBy("v.created_at DESC")
	p := mustPaginate(q, max(1, c.PostInt("page", 1)), 50)
	return c.Json(web.M{"status": 200, "items": prepare(p.Items), "count": p.Total})
}
