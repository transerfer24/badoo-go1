package commands

/*
Импорт боевой базы старого бэкенда (SQLite с hermes1, снимок — make fetch-db) в базу проекта (MySQL хостинга):

	./app import data/amura.db              # в пустую базу
	./app import data/amura.db --wipe       # сначала стереть всё, что уже есть (повторный прогон)
	./app import data/amura.db --photos=amur/prod   # папка старых фото в общем бакете S3 (по умолчанию amur/prod)

Что меняется по дороге (PLAN.md, этап 6):
  - даты: ISO-строки '2026-10-12T10:47:41Z' → DATETIME; пустая строка в read_at / premium_until / checked_in_at → NULL;
  - photos.file и users.verify_file: голое имя файла → полный ключ в бакете (<photos>/photos/<имя>), сами файлы
    не копируются — новый код отдаёт их из того же бакета по ключу;
  - users.auth_key — новая колонка (remember-me веба), заполняется случайно;
  - sessions переносятся (приложение у людей остаётся залогиненным), auth_flows — нет (попытки входа живут 30 минут);
  - id сохраняются как были — ссылки между таблицами не пересчитываются.
*/

import (
	"fmt"
	"strings"
	"time"

	"badoo/components/chat"
	"badoo/components/neuronet"

	"whales.team/go1/app"
	"whales.team/go1/console"
	"whales.team/go1/db"
	_ "whales.team/go1/db/drivers/sqlite" // исходник импорта — SQLite; в проде база MySQL (main.go)
)

const oldTs = "2006-01-02T15:04:05Z"

func init() {
	All = append(All, console.Command{Name: "import", Description: "Импорт базы старого бэкенда: import <amura.db> [--wipe] [--photos=amur/prod]", Run: runImport})
}

func runImport(args console.Args) error {
	path := args.Get(0)
	if path == "" {
		return fmt.Errorf("укажите файл SQLite: ./app import data/amura.db")
	}
	prefix := strings.Trim(args.Flag("photos", "amur/prod"), "/")
	src, err := db.Open(db.Config{DSN: "file:" + path + "?mode=ro", Logger: app.Log})
	if err != nil {
		return fmt.Errorf("исходная база: %w", err)
	}
	defer src.Close()

	// порядок — от независимых к зависимым; стирание в обратном
	tables := []string{"users", "photos", "votes", "matches", "messages", "visits", "blocks", "reports", "sessions", "meetups", "meetup_going", "push_tokens"}
	if n := app.Db.CreateCommand("SELECT COUNT(*) FROM {{users}}").MustInt(); n > 0 {
		if args.Flag("wipe") != "1" {
			return fmt.Errorf("в базе уже %d анкет — повторите с --wipe, чтобы стереть и залить заново", n)
		}
		for i := len(tables) - 1; i >= 0; i-- {
			app.Db.CreateCommand("DELETE FROM {{" + tables[i] + "}}").MustExecute()
		}
		fmt.Println("Старые строки стёрты")
	}

	ts := func(r db.Row, k string) any { // ISO-строка → время; пусто → NULL
		s := strings.TrimSpace(r.Str(k))
		if s == "" {
			return nil
		}
		t, err := time.Parse(oldTs, s)
		if err != nil {
			return nil
		}
		return t.UTC()
	}
	key := func(kind, name string) string {
		if name == "" {
			return ""
		}
		return prefix + "/" + kind + "/" + name
	}
	now := time.Now().UTC()

	return app.Db.Transaction(func(tx *db.Tx) error {
		counts := map[string]int{}
		rows := func(table string) []db.Row {
			return src.CreateCommand("SELECT * FROM " + table).MustAll()
		}
		for _, r := range rows("users") {
			tx.MustInsert("{{users}}", db.H{
				"id": r.Int64("id"), "email": r.Str("email"), "neuronet_uid": nullInt(r, "neuronet_uid"), "pass_hash": r.Str("pass_hash"),
				"auth_key": neuronet.RandToken(24), "name": r.Str("name"), "birthdate": r.Str("birthdate"), "gender": r.Str("gender"),
				"seeking": r.Str("seeking"), "bio": r.Str("bio"), "city": r.Str("city"), "lat": r.Float("lat"), "lng": r.Float("lng"),
				"height": r.Int("height"), "intent": r.Str("intent"), "interests": r.Str("interests"), "verified": r.Int("verified"),
				"verify_file": key("private", r.Str("verify_file")), "premium_until": ts(r, "premium_until"), "invisible": r.Int("invisible") == 1,
				"age_min": r.Int("age_min"), "age_max": r.Int("age_max"), "max_km": r.Int("max_km"), "verified_only": r.Int("verified_only") == 1,
				"onboarded": r.Int("onboarded") == 1, "created_at": orNow(ts(r, "created_at"), now), "last_seen": orNow(ts(r, "last_seen"), now),
			})
			counts["users"]++
		}
		for _, r := range rows("photos") {
			tx.MustInsert("{{photos}}", db.H{"id": r.Int64("id"), "user_id": r.Int64("user_id"), "file": key("photos", r.Str("file")), "pos": r.Int("pos")})
			counts["photos"]++
		}
		for _, r := range rows("votes") {
			tx.CreateCommand("INSERT INTO {{votes}} (voter_id, target_id, liked, created_at) VALUES (:a, :b, :l, :t)",
				db.P{":a": r.Int64("voter_id"), ":b": r.Int64("target_id"), ":l": r.Int("liked"), ":t": orNow(ts(r, "created_at"), now)}).MustExecute()
			counts["votes"]++
		}
		for _, r := range rows("matches") {
			tx.MustInsert("{{matches}}", db.H{"id": r.Int64("id"), "user_a": r.Int64("user_a"), "user_b": r.Int64("user_b"), "created_at": orNow(ts(r, "created_at"), now)})
			counts["matches"]++
		}
		for _, r := range rows("messages") {
			tx.MustInsert("{{messages}}", db.H{"id": r.Int64("id"), "match_id": r.Int64("match_id"), "sender_id": r.Int64("sender_id"), "body": r.Str("body"),
				"created_at": orNow(ts(r, "created_at"), now), "read_at": ts(r, "read_at")})
			counts["messages"]++
		}
		for _, r := range rows("visits") {
			tx.CreateCommand("INSERT INTO {{visits}} (visitor_id, visited_id, at) VALUES (:a, :b, :t)",
				db.P{":a": r.Int64("visitor_id"), ":b": r.Int64("visited_id"), ":t": orNow(ts(r, "at"), now)}).MustExecute()
			counts["visits"]++
		}
		for _, r := range rows("blocks") {
			tx.CreateCommand("INSERT INTO {{blocks}} (blocker_id, blocked_id) VALUES (:a, :b)", db.P{":a": r.Int64("blocker_id"), ":b": r.Int64("blocked_id")}).MustExecute()
			counts["blocks"]++
		}
		for _, r := range rows("reports") {
			tx.MustInsert("{{reports}}", db.H{"id": r.Int64("id"), "reporter_id": r.Int64("reporter_id"), "reported_id": r.Int64("reported_id"),
				"reason": r.Str("reason"), "created_at": orNow(ts(r, "created_at"), now)})
			counts["reports"]++
		}
		for _, r := range rows("sessions") {
			exp := ts(r, "expires_at")
			if exp == nil || !exp.(time.Time).After(now) {
				continue // просроченные не нужны
			}
			tx.CreateCommand("INSERT INTO {{sessions}} (token, user_id, platform, expires_at, created_at) VALUES (:t, :u, '', :e, :c)",
				db.P{":t": r.Str("token"), ":u": r.Int64("user_id"), ":e": exp, ":c": now}).MustExecute()
			counts["sessions"]++
		}
		for _, r := range rows("meetups") {
			tx.MustInsert("{{meetups}}", db.H{
				"id": r.Int64("id"), "title": r.Str("title"), "about": r.Str("about"), "kind": r.Str("kind"), "place": r.Str("place"),
				"address": r.Str("address"), "city": r.Str("city"), "lat": r.Float("lat"), "lng": r.Float("lng"),
				"starts_at": orNow(ts(r, "starts_at"), now), "ends_at": orNow(ts(r, "ends_at"), now), "capacity": r.Int("capacity"),
				"price": r.Str("price"), "host_id": nullInt(r, "host_id"), "demo": r.Int("demo") == 1, "created_at": orNow(ts(r, "created_at"), now),
			})
			counts["meetups"]++
		}
		for _, r := range rows("meetup_going") {
			tx.CreateCommand("INSERT INTO {{meetup_going}} (meetup_id, user_id, checked_in_at, created_at) VALUES (:m, :u, :c, :t)",
				db.P{":m": r.Int64("meetup_id"), ":u": r.Int64("user_id"), ":c": ts(r, "checked_in_at"), ":t": orNow(ts(r, "created_at"), now)}).MustExecute()
			counts["meetup_going"]++
		}
		for _, r := range rows("push_tokens") {
			tx.CreateCommand("INSERT INTO {{push_tokens}} (token, user_id, platform, created_at) VALUES (:t, :u, :p, :c)",
				db.P{":t": r.Str("token"), ":u": r.Int64("user_id"), ":p": r.Str("platform"), ":c": orNow(ts(r, "created_at"), now)}).MustExecute()
			counts["push_tokens"]++
		}
		for _, t := range tables {
			fmt.Printf("  %-13s %6d\n", t, counts[t])
		}
		chat.Rebuild(tx) // шапки пар (последнее сообщение, непрочитанные) — по перенесённым сообщениям
		fmt.Printf("Готово: фото — в бакете под %s/photos/, старые id сохранены\n", prefix)
		return nil
	})
}

func nullInt(r db.Row, k string) any {
	if r.IsNull(k) || r.Int64(k) == 0 {
		return nil
	}
	return r.Int64(k)
}

func orNow(v any, now time.Time) any {
	if v == nil {
		return now
	}
	return v
}
