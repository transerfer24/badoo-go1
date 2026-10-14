package migrations

import (
	"whales.team/go1/db"
	"whales.team/go1/rbac"
)

/*
Схема Amura — перенос backend/db.go (+ meetups.go, push.go) из SQLite-версии один в один, но через Schema Builder:
одна миграция работает и на MySQL хостинга, и на SQLite в тестах. Составные первичные ключи
(votes, visits, blocks, meetup_going) и строковые (auth_flows.id, sessions.token, push_tokens.token) — уникальными
индексами: так делает ядро (rbac), и так же работает на обеих СУБД.

Что изменилось относительно SQLite:
  - даты: были ISO-строками (TEXT '…Z'), стали DATETIME; «пусто» = NULL (read_at, checked_in_at, premium_until);
  - users.auth_key — новая колонка для remember-me cookie веб-версии (у мобильного — Bearer из sessions);
  - CHECK-ограничения SQLite (gender IN …) — в коде экшенов (validate), а не в схеме: MySQL 5.7 их игнорирует;
  - частичный индекс users_neuronet WHERE neuronet_uid IS NOT NULL → обычный уникальный (NULL в MySQL уникальность не ломает).
*/
func init() {
	for _, m := range rbac.Migrations() { // auth_* — роль admin для /admin
		Register(m)
	}

	Register(db.Migration{
		Version: "m261014_190000_init",
		Up: func(m *db.Schema) error {
			if err := m.CreateTable("{{users}}", []*db.Column{
				db.BigPK("id"),
				db.String("email", 190).NotNull(),
				db.BigInt("neuronet_uid").Nullable(),
				db.String("pass_hash", 255).NotNull().DefaultValue(""), // только локальные тестовые аккаунты (dev)
				db.String("auth_key", 64).NotNull().DefaultValue(""),
				db.String("name", 100).NotNull().DefaultValue(""),
				db.String("birthdate", 10).NotNull().DefaultValue(""),
				db.String("gender", 1).NotNull().DefaultValue(""),     // '', m, f, x
				db.String("seeking", 3).NotNull().DefaultValue("any"), // m, f, any
				db.Text("bio").Nullable(),
				db.String("city", 100).NotNull().DefaultValue(""),
				db.Double("lat").NotNull().DefaultValue(0),
				db.Double("lng").NotNull().DefaultValue(0),
				db.Int("height").NotNull().DefaultValue(0),
				db.String("intent", 32).NotNull().DefaultValue(""),
				db.String("interests", 500).NotNull().DefaultValue(""),
				db.SmallInt("verified").NotNull().DefaultValue(0), // 0 нет, 1 на проверке, 2 подтверждён
				db.String("verify_file", 255).NotNull().DefaultValue(""),
				db.DateTime("premium_until").Nullable(),
				db.Bool("invisible").NotNull().DefaultValue(false),
				db.Int("age_min").NotNull().DefaultValue(18),
				db.Int("age_max").NotNull().DefaultValue(45),
				db.Int("max_km").NotNull().DefaultValue(50),
				db.Bool("verified_only").NotNull().DefaultValue(false),
				db.Bool("onboarded").NotNull().DefaultValue(false),
				db.DateTime("created_at").NotNull(),
				db.DateTime("last_seen").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_users_email", "{{users}}", []string{"email"}, true); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_users_neuronet", "{{users}}", []string{"neuronet_uid"}, true); err != nil {
				return err
			}
			if err := m.CreateIndex("users_geo", "{{users}}", []string{"lat", "lng"}); err != nil {
				return err
			}

			if err := m.CreateTable("{{photos}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("user_id").NotNull(),
				db.String("file", 255).NotNull(),
				db.Int("pos").NotNull().DefaultValue(0),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("photos_user", "{{photos}}", []string{"user_id", "pos"}); err != nil {
				return err
			}

			if err := m.CreateTable("{{votes}}", []*db.Column{
				db.BigInt("voter_id").NotNull(),
				db.BigInt("target_id").NotNull(),
				db.Bool("liked").NotNull(),
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_votes", "{{votes}}", []string{"voter_id", "target_id"}, true); err != nil {
				return err
			}
			if err := m.CreateIndex("votes_target", "{{votes}}", []string{"target_id", "liked"}); err != nil {
				return err
			}

			if err := m.CreateTable("{{matches}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("user_a").NotNull(), // всегда user_a < user_b — держит код (matchPair)
				db.BigInt("user_b").NotNull(),
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_matches_pair", "{{matches}}", []string{"user_a", "user_b"}, true); err != nil {
				return err
			}
			if err := m.CreateIndex("matches_b", "{{matches}}", []string{"user_b"}); err != nil {
				return err
			}

			if err := m.CreateTable("{{messages}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("match_id").NotNull(),
				db.BigInt("sender_id").NotNull(),
				db.Text("body").NotNull(),
				db.DateTime("created_at").NotNull(),
				db.DateTime("read_at").Nullable(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("messages_match", "{{messages}}", []string{"match_id", "id"}); err != nil {
				return err
			}

			if err := m.CreateTable("{{visits}}", []*db.Column{
				db.BigInt("visitor_id").NotNull(),
				db.BigInt("visited_id").NotNull(),
				db.DateTime("at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_visits", "{{visits}}", []string{"visitor_id", "visited_id"}, true); err != nil {
				return err
			}
			if err := m.CreateIndex("visits_visited", "{{visits}}", []string{"visited_id", "at"}); err != nil {
				return err
			}

			if err := m.CreateTable("{{blocks}}", []*db.Column{
				db.BigInt("blocker_id").NotNull(),
				db.BigInt("blocked_id").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_blocks", "{{blocks}}", []string{"blocker_id", "blocked_id"}, true); err != nil {
				return err
			}

			if err := m.CreateTable("{{reports}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("reporter_id").NotNull(),
				db.BigInt("reported_id").NotNull(),
				db.String("reason", 255).NotNull(),
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}

			// Попытка входа по email (шаги: email → пароль или код → регистрация). Email и тикет Neuronet живут
			// здесь, клиент носит только id попытки — иначе код, отправленный на один адрес, можно было бы
			// подтвердить для другого.
			if err := m.CreateTable("{{auth_flows}}", []*db.Column{
				db.String("id", 64).NotNull(),
				db.String("email", 190).NotNull(),
				db.String("step", 20).NotNull(),
				db.Bool("local").NotNull().DefaultValue(false), // вход локальным паролем (тестовые аккаунты в dev)
				db.String("ticket", 255).NotNull().DefaultValue(""),
				db.Bool("require_invite").NotNull().DefaultValue(false),
				db.BigInt("resend_at").NotNull().DefaultValue(0),
				db.BigInt("expires_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_auth_flows", "{{auth_flows}}", []string{"id"}, true); err != nil {
				return err
			}

			// Bearer-токены мобильного приложения (веб-версия — cookie-сессии Go1).
			if err := m.CreateTable("{{sessions}}", []*db.Column{
				db.String("token", 64).NotNull(),
				db.BigInt("user_id").NotNull(),
				db.String("platform", 20).NotNull().DefaultValue(""),
				db.DateTime("expires_at").NotNull(),
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_sessions_token", "{{sessions}}", []string{"token"}, true); err != nil {
				return err
			}
			if err := m.CreateIndex("sessions_user", "{{sessions}}", []string{"user_id"}); err != nil {
				return err
			}

			// Встречи (backend/meetups.go)
			if err := m.CreateTable("{{meetups}}", []*db.Column{
				db.BigPK("id"),
				db.String("title", 200).NotNull(),
				db.Text("about").Nullable(),
				db.String("kind", 32).NotNull(), // drinks, beach, … — справочник в коде (meetupKinds)
				db.String("place", 200).NotNull(),
				db.String("address", 255).NotNull().DefaultValue(""),
				db.String("city", 100).NotNull().DefaultValue(""),
				db.Double("lat").NotNull().DefaultValue(0),
				db.Double("lng").NotNull().DefaultValue(0),
				db.DateTime("starts_at").NotNull(),
				db.DateTime("ends_at").NotNull(),
				db.Int("capacity").NotNull().DefaultValue(0), // 0 — без ограничения
				db.String("price", 50).NotNull().DefaultValue(""),
				db.BigInt("host_id").Nullable(),
				db.Bool("demo").NotNull().DefaultValue(false), // сгенерировано для теста (AMURA_DEV)
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("meetups_time", "{{meetups}}", []string{"starts_at"}); err != nil {
				return err
			}
			if err := m.CreateIndex("meetups_geo", "{{meetups}}", []string{"lat", "lng"}); err != nil {
				return err
			}
			if err := m.CreateTable("{{meetup_going}}", []*db.Column{
				db.BigInt("meetup_id").NotNull(),
				db.BigInt("user_id").NotNull(),
				db.DateTime("checked_in_at").Nullable(),
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_meetup_going", "{{meetup_going}}", []string{"meetup_id", "user_id"}, true); err != nil {
				return err
			}
			if err := m.CreateIndex("meetup_going_user", "{{meetup_going}}", []string{"user_id"}); err != nil {
				return err
			}

			// Push-токены Expo (backend/push.go)
			if err := m.CreateTable("{{push_tokens}}", []*db.Column{
				db.String("token", 255).NotNull(),
				db.BigInt("user_id").NotNull(),
				db.String("platform", 20).NotNull().DefaultValue(""),
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_push_tokens_token", "{{push_tokens}}", []string{"token"}, true); err != nil {
				return err
			}
			return m.CreateIndex("push_tokens_user", "{{push_tokens}}", []string{"user_id"})
		},
		Down: func(m *db.Schema) error {
			for _, t := range []string{"push_tokens", "meetup_going", "meetups", "sessions", "auth_flows", "reports",
				"blocks", "visits", "messages", "matches", "votes", "photos", "users"} {
				if err := m.DropTableIfExists("{{" + t + "}}"); err != nil {
					return err
				}
			}
			return nil
		},
	})
}
