package migrations

import "whales.team/go1/db"

/*
Разделы админки, которые только читают данные приложения (мобильное приложение их не замечает):

  - группа «Аналитика»: Воронка и удержание (analytics), Симпатии и антиспам (votes), Гости (visits);
  - в «Систему»: Входы и сессии (sessions), Состояние системы (system).

Только добавление: индексы под выборки за период и строки разделов. Миграция повторяемая — раздел или группу,
заведённые раньше руками в «Права → Разделы», не дублирует. Новые разделы сразу открываются суперадминам
(как при сохранении раздела в «Правах»).
*/

// разделы этой миграции: группа "" — «Система» (id 2 из m261017_120000_control), "analytics" — новая группа
var analyticsSections = []struct {
	action, name, group string
	priority            int
}{
	{"analytics", "Воронка и удержание", "analytics", 30},
	{"votes", "Симпатии и антиспам", "analytics", 29},
	{"visits", "Гости", "analytics", 28},
	{"sessions", "Входы и сессии", "", 25},
	{"system", "Состояние системы", "", 5},
}

const analyticsGroup = "Аналитика"

func init() {
	Register(db.Migration{
		Version: "m261018_120000_control_analytics",
		Up: func(m *db.Schema) error {
			// выборки «за период» по голосам и гостям, «первое сообщение» в воронке
			for _, ix := range []struct{ name, table, col string }{
				{"votes_created", "{{votes}}", "created_at"},
				{"visits_at", "{{visits}}", "at"},
				{"messages_sender", "{{messages}}", "sender_id"},
			} {
				if err := m.CreateIndex(ix.name, ix.table, []string{ix.col}); err != nil {
					return err
				}
			}

			cid := m.CreateCommand("SELECT id FROM {{control_categories}} WHERE name = :n", db.P{":n": analyticsGroup}).MustInt()
			if cid == 0 {
				if err := m.Insert("{{control_categories}}", db.H{"name": analyticsGroup}); err != nil {
					return err
				}
				cid = m.CreateCommand("SELECT id FROM {{control_categories}} WHERE name = :n", db.P{":n": analyticsGroup}).MustInt()
			}
			for _, s := range analyticsSections {
				if m.CreateCommand("SELECT id FROM {{control_actions}} WHERE action = :a", db.P{":a": s.action}).MustInt() > 0 {
					continue
				}
				group := int64(2)
				if s.group == "analytics" {
					group = cid
				}
				if err := m.Insert("{{control_actions}}", db.H{"action": s.action, "name": s.name, "cid": group,
					"priority": s.priority, "status": 1, "locked": 0}); err != nil {
					return err
				}
			}
			return m.Execute(`INSERT INTO {{control_rules}} (uid, action_id, level, status)
				SELECT sa.uid, a.id, 1, 1 FROM {{control_superadmins}} sa
				INNER JOIN {{control_actions}} a ON a.action IN ('analytics', 'votes', 'visits', 'sessions', 'system')
				WHERE NOT EXISTS (SELECT 1 FROM {{control_rules}} r WHERE r.uid = sa.uid AND r.action_id = a.id)`)
		},
		Down: func(m *db.Schema) error {
			if err := m.Execute(`DELETE FROM {{control_rules}} WHERE action_id IN
				(SELECT id FROM {{control_actions}} WHERE action IN ('analytics', 'votes', 'visits', 'sessions', 'system'))`); err != nil {
				return err
			}
			if err := m.Execute("DELETE FROM {{control_actions}} WHERE action IN ('analytics', 'votes', 'visits', 'sessions', 'system')"); err != nil {
				return err
			}
			if err := m.Execute("DELETE FROM {{control_categories}} WHERE name = :n", db.P{":n": analyticsGroup}); err != nil {
				return err
			}
			for _, ix := range [][2]string{{"messages_sender", "{{messages}}"}, {"visits_at", "{{visits}}"}, {"votes_created", "{{votes}}"}} {
				if err := m.DropIndex(ix[0], ix[1]); err != nil {
					return err
				}
			}
			return nil
		},
	})
}
