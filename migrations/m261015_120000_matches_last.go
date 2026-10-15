package migrations

import "whales.team/go1/db"

/*
Денормализация для экрана чатов и счётчиков (PLAN.md, «на нагрузке»): вместо четырёх коррелированных подзапросов
к messages на каждую пару — колонки прямо в matches, обновляемые при отправке и прочтении:

	last_msg_id   id последнего сообщения (NULL — ещё не писали)
	last_at       его время (для сортировки без JOIN; у новой пары = created_at)
	unread_a/b    непрочитанные у user_a / user_b

Экран «Чаты» — один запрос по индексу (user_a / user_b) + JOIN messages по PK; счётчик непрочитанных —
SUM по тем же индексам. Заполнение по существующим сообщениям — здесь же.
*/
func init() {
	Register(db.Migration{
		Version: "m261015_120000_matches_last",
		Up: func(m *db.Schema) error {
			for _, col := range []*db.Column{
				db.BigInt("last_msg_id").Nullable(),
				db.DateTime("last_at").Nullable(),
				db.Int("unread_a").NotNull().DefaultValue(0),
				db.Int("unread_b").NotNull().DefaultValue(0),
			} {
				if err := m.AddColumn("{{matches}}", col); err != nil {
					return err
				}
			}
			if err := m.CreateIndex("matches_a_last", "{{matches}}", []string{"user_a", "last_at"}); err != nil {
				return err
			}
			if err := m.CreateIndex("matches_b_last", "{{matches}}", []string{"user_b", "last_at"}); err != nil {
				return err
			}
			// заполнить по тому, что уже есть
			return m.Execute(`UPDATE {{matches}} SET
				last_msg_id = (SELECT MAX(id) FROM {{messages}} WHERE match_id = {{matches}}.id),
				last_at = COALESCE((SELECT MAX(created_at) FROM {{messages}} WHERE match_id = {{matches}}.id), created_at),
				unread_a = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = {{matches}}.id AND sender_id = {{matches}}.user_b AND read_at IS NULL),
				unread_b = (SELECT COUNT(*) FROM {{messages}} WHERE match_id = {{matches}}.id AND sender_id = {{matches}}.user_a AND read_at IS NULL)`)
		},
		Down: func(m *db.Schema) error {
			_ = m.DropIndex("matches_a_last", "{{matches}}")
			_ = m.DropIndex("matches_b_last", "{{matches}}")
			for _, c := range []string{"last_msg_id", "last_at", "unread_a", "unread_b"} {
				if err := m.DropColumn("{{matches}}", c); err != nil {
					return err
				}
			}
			return nil
		},
	})
}
