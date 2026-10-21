package migrations

import "whales.team/go1/db"

/*
Языки анкеты: справочник languages (правится в /control/languages) и выбор человека в users.languages.

	languages        code (ISO 639-1, «uk»), name (по-русски, «Украинский»), native («Українська»),
	                 pos (порядок в списке: частые сверху), active (0 — скрыт из выбора, у кого выбран — остаётся)
	users.languages  «uk:fluent,ru:fluent,en:speak» — код:уровень через запятую, до 5, в порядке выбора;
	                 уровни — users.LangLevels (fluent свободно, speak говорю, read читаю, learn учу)

Начальный список — langSeed (m261022_languages_seed.go). Раздел админки — в control_actions.
*/
func init() {
	Register(db.Migration{
		Version: "m261022_120000_languages",
		Up: func(m *db.Schema) error {
			if err := m.CreateTable("{{languages}}", []*db.Column{
				db.PK("id"),
				db.String("code", 8).NotNull(),
				db.String("name", 64).NotNull(),
				db.String("native", 64).NotNull().DefaultValue(""),
				db.Int("pos").NotNull().DefaultValue(1000),
				db.SmallInt("active").NotNull().DefaultValue(1),
			}); err != nil {
				return err
			}
			if err := m.CreateIndex("uq_languages_code", "{{languages}}", []string{"code"}, true); err != nil {
				return err
			}
			for i, l := range langSeed {
				if err := m.Insert("{{languages}}", db.H{"code": l[0], "name": l[1], "native": l[2], "pos": (i + 1) * 10, "active": 1}); err != nil {
					return err
				}
			}
			if err := m.AddColumn("{{users}}", db.String("languages", 255).NotNull().DefaultValue("")); err != nil {
				return err
			}
			// раздел админки «Языки» в группе «Система» (cid 2), как остальные справочники
			return m.Insert("{{control_actions}}", db.H{"action": "languages", "name": "Языки", "cid": 2, "priority": 5, "status": 1, "locked": 0})
		},
		Down: func(m *db.Schema) error {
			_ = m.Execute("DELETE FROM {{control_actions}} WHERE action = 'languages'")
			_ = m.DropColumn("{{users}}", "languages")
			return m.DropTable("{{languages}}")
		},
	})
}
