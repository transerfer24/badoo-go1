package migrations

import "whales.team/go1/db"

/*
Индексы под горячие запросы (docs/performance-audit.md, раздел 6). Только добавление — откат выкладки им не мешает,
API приложения не меняется. Старые индексы (votes_target, users_geo) пока не удаляем: убрать после того,
как колода и «Рядом» переедут на users_disc.
*/

var perfIndexes = []struct {
	name, table string
	cols        []string
}{
	{"votes_target_liked_at", "{{votes}}", []string{"target_id", "liked", "created_at"}},  // симпатии, бейдж
	{"votes_voter_liked_at", "{{votes}}", []string{"voter_id", "liked", "created_at"}},     // вернуть анкету
	{"blocks_blocked", "{{blocks}}", []string{"blocked_id", "blocker_id"}},                 // обратные блокировки
	{"users_disc", "{{users}}", []string{"onboarded", "banned", "lat", "lng", "birthdate", "gender", "seeking"}}, // колода, «Рядом»
	{"messages_match_read", "{{messages}}", []string{"match_id", "read_at"}},               // отметка прочитанного
	{"messages_created", "{{messages}}", []string{"created_at"}},                           // дашборд, аналитика
	{"matches_created", "{{matches}}", []string{"created_at"}},                             // дашборд, аналитика
	{"meetups_ends", "{{meetups}}", []string{"ends_at"}},                                   // встречи рядом
	{"meetups_host", "{{meetups}}", []string{"host_id", "ends_at"}},                        // лимит событий, удаление
	{"reports_reporter", "{{reports}}", []string{"reporter_id"}},                           // удаление аккаунта, админка
	{"auth_flows_expires", "{{auth_flows}}", []string{"expires_at"}},                       // чистка просроченных попыток
	{"auth_flows_email", "{{auth_flows}}", []string{"email"}},                              // админка: попытки входа
	{"sessions_expires", "{{sessions}}", []string{"expires_at"}},                           // чистка просроченных токенов
}

func init() {
	Register(db.Migration{
		Version: "m261018_140000_perf_indexes",
		Up: func(m *db.Schema) error {
			for _, ix := range perfIndexes {
				if err := m.CreateIndex(ix.name, ix.table, ix.cols); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(m *db.Schema) error {
			for i := len(perfIndexes) - 1; i >= 0; i-- {
				if err := m.DropIndex(perfIndexes[i].name, perfIndexes[i].table); err != nil {
					return err
				}
			}
			return nil
		},
	})
}
