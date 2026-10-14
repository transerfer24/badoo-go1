// Package commands — консольные команды и cron-задачи проекта.
// Запуск: `go run . cron` (планировщик), `go run . queue/work` (очередь), `go run . help` (все команды).
package commands

import (
	"context"
	"log/slog"

	"whales.team/go1/cron"
)

func init() {
	cron.Daily("03:00", "example.ping", func(ctx context.Context) error {
		slog.Info("cron: пример задачи, замени на свою")
		return nil
	})
}
