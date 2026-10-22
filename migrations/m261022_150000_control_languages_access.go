package migrations

import "whales.team/go1/db"

/*
Раздел «Языки» (m261022_120000_languages) завёлся в control_actions, но права на него никому не выдались — в меню
админки его не было видно даже суперадмину. Открываем суперадминам, как при сохранении раздела в «Правах».
Повторяемая: уже выданное право не дублирует. Приложение не задевает.
*/

func init() {
	Register(db.Migration{
		Version: "m261022_150000_control_languages_access",
		Up: func(m *db.Schema) error {
			return m.Execute(`INSERT INTO {{control_rules}} (uid, action_id, level, status)
				SELECT sa.uid, a.id, 1, 1 FROM {{control_superadmins}} sa
				INNER JOIN {{control_actions}} a ON a.action = 'languages'
				WHERE NOT EXISTS (SELECT 1 FROM {{control_rules}} r WHERE r.uid = sa.uid AND r.action_id = a.id)`)
		},
		Down: func(m *db.Schema) error {
			return m.Execute(`DELETE FROM {{control_rules}} WHERE action_id IN
				(SELECT id FROM {{control_actions}} WHERE action = 'languages')`)
		},
	})
}
