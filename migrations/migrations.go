// Package migrations — миграции проекта. Каждый файл регистрирует себя через Register в init().
// Запуск: `go run . migrate`, откат: `go run . migrate/down`.
package migrations

import "whales.team/go1/db"

var all []db.Migration

// Register добавляет миграцию в список.
func Register(m db.Migration) { all = append(all, m) }

// All возвращает все миграции проекта.
func All() []db.Migration { return all }
