package migrations

import "whales.team/go1/db"

/*
Админка /control (modules/control) — как в StockGo1:

  - control_categories / control_actions / control_rules / control_rules_history / control_superadmins — разделы,
    группы меню и права админов (раздел = контроллер /control/<раздел>);
  - control_log — журнал действий админов (блокировки, верификация, удаление фото и сообщений…);
  - users.banned / ban_reason / banned_at / banned_by — блокировка: такой пользователь не входит (ни сайт, ни
    приложение), его не видно в колоде, «Рядом», чатах, симпатиях и на встречах;
  - reports.status / resolved_by / resolved_at — разбор жалоб: 0 новая, 1 приняты меры, 2 отклонена.

Первый суперадмин — командой: ./badoo-app control/super <email или id> (локально: go run . control/super …,
на хостинге: make control-super WHO=<email>).
*/
func init() {
	Register(db.Migration{
		Version: "m261017_120000_control",
		Up: func(m *db.Schema) error {
			for _, col := range []*db.Column{
				db.Bool("banned").NotNull().DefaultValue(false),
				db.String("ban_reason", 255).NotNull().DefaultValue(""),
				db.DateTime("banned_at").Nullable(),
				db.BigInt("banned_by").Nullable(),
			} {
				if err := m.AddColumn("{{users}}", col); err != nil {
					return err
				}
			}
			if err := m.CreateIndex("users_created", "{{users}}", []string{"created_at"}); err != nil {
				return err
			}
			if err := m.CreateIndex("users_last_seen", "{{users}}", []string{"last_seen"}); err != nil {
				return err
			}
			if err := m.CreateIndex("users_verified", "{{users}}", []string{"verified"}); err != nil { // очередь селфи
				return err
			}
			for _, col := range []*db.Column{
				db.SmallInt("status").NotNull().DefaultValue(0),
				db.BigInt("resolved_by").Nullable(),
				db.DateTime("resolved_at").Nullable(),
			} {
				if err := m.AddColumn("{{reports}}", col); err != nil {
					return err
				}
			}
			if err := m.CreateIndex("reports_reported", "{{reports}}", []string{"reported_id", "status"}); err != nil {
				return err
			}
			if err := m.CreateIndex("reports_status", "{{reports}}", []string{"status", "id"}); err != nil {
				return err
			}

			if err := m.CreateTable("{{control_categories}}", []*db.Column{
				db.BigPK("id"),
				db.String("name", 50).NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateTable("{{control_actions}}", []*db.Column{
				db.BigPK("id"),
				db.String("action", 30).NotNull(), // адрес раздела: /control/<action>
				db.String("name", 30).NotNull(),
				db.BigInt("cid").NotNull().DefaultValue(0), // группа меню (0 — без группы)
				db.Int("priority").NotNull().DefaultValue(0),
				db.SmallInt("status").NotNull().DefaultValue(1),
				db.SmallInt("locked").NotNull().DefaultValue(0), // 1 — право не меняется из админки
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_control_actions", "{{control_actions}}", []string{"action"}, true); err != nil {
				return err
			}
			if err := m.CreateTable("{{control_rules}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("uid").NotNull(),
				db.BigInt("action_id").NotNull(),
				db.SmallInt("level").NotNull().DefaultValue(1),
				db.SmallInt("status").NotNull().DefaultValue(1),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_control_rules", "{{control_rules}}", []string{"uid", "action_id"}, true); err != nil {
				return err
			}
			if err := m.CreateTable("{{control_rules_history}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("user_id").NotNull(),
				db.BigInt("parent_uid").NotNull(),
				db.BigInt("action_id").NotNull(),
				db.SmallInt("type").NotNull(), // 1 включили, 0 выключили
				db.DateTime("date").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("control_rules_history_user", "{{control_rules_history}}", []string{"user_id", "id"}); err != nil {
				return err
			}
			if err := m.CreateTable("{{control_superadmins}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("uid").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_control_superadmins", "{{control_superadmins}}", []string{"uid"}, true); err != nil {
				return err
			}
			if err := m.CreateTable("{{control_log}}", []*db.Column{
				db.BigPK("id"),
				db.BigInt("admin_id").NotNull(),
				db.BigInt("user_id").NotNull().DefaultValue(0), // над кем (0 — не о пользователе)
				db.String("action", 40).NotNull(),              // ban, unban, verify, photo-delete, …
				db.String("details", 500).NotNull().DefaultValue(""),
				db.String("ip", 45).NotNull().DefaultValue(""),
				db.DateTime("created_at").NotNull(),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("control_log_user", "{{control_log}}", []string{"user_id", "id"}); err != nil {
				return err
			}
			if err := m.CreateIndex("control_log_admin", "{{control_log}}", []string{"admin_id", "id"}); err != nil {
				return err
			}

			// Группы меню и разделы. id групп — явно, чтобы разделы на них ссылались.
			for _, c := range []db.H{{"id": 1, "name": "Модерация"}, {"id": 2, "name": "Система"}} {
				if err := m.Insert("{{control_categories}}", c); err != nil {
					return err
				}
			}
			for _, a := range []db.H{
				{"action": "index", "name": "Дашборд", "cid": 0, "priority": 100},
				{"action": "users", "name": "Пользователи", "cid": 0, "priority": 90},
				{"action": "chats", "name": "Переписка", "cid": 0, "priority": 80},
				{"action": "meetups", "name": "Встречи", "cid": 0, "priority": 70},
				{"action": "reports", "name": "Жалобы", "cid": 1, "priority": 60},
				{"action": "verification", "name": "Верификация", "cid": 1, "priority": 50},
				{"action": "photos", "name": "Фото", "cid": 1, "priority": 40},
				{"action": "log", "name": "Журнал действий", "cid": 2, "priority": 20},
				{"action": "permissions", "name": "Права", "cid": 2, "priority": 10},
			} {
				a["status"], a["locked"] = 1, 0
				if err := m.Insert("{{control_actions}}", a); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(m *db.Schema) error {
			for _, t := range []string{"control_log", "control_superadmins", "control_rules_history", "control_rules", "control_actions", "control_categories"} {
				if err := m.DropTableIfExists("{{" + t + "}}"); err != nil {
					return err
				}
			}
			for _, c := range []string{"resolved_at", "resolved_by", "status"} {
				if err := m.DropColumn("{{reports}}", c); err != nil {
					return err
				}
			}
			for _, c := range []string{"banned_by", "banned_at", "ban_reason", "banned"} {
				if err := m.DropColumn("{{users}}", c); err != nil {
					return err
				}
			}
			return nil
		},
	})
}
