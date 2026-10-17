// Package commands — консольные команды и cron-задачи проекта.
// Запуск: `go run . cron` (планировщик), `go run . queue/work` (очередь), `go run . help` (все команды).
package commands

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"badoo/components/meetups"
	"badoo/modules/control"

	"whales.team/go1/console"
	"whales.team/go1/cron"
)

func init() {
	// Тестовые события в dev-режиме (AMURA_DEV=1): раз в 6 часов досоздаются на неделю вперёд
	// (было demoMeetupLoop в meetups.go). Вручную: ./app meetups/demo.
	cron.Every(6*time.Hour, "meetups.demo", func(ctx context.Context) error {
		if n := meetups.EnsureDemo(); n > 0 {
			slog.Info("cron: тестовые события", "added", n)
		}
		return nil
	})
}

// All — консольные команды проекта (config.App.Commands).
var All = []console.Command{
	{Name: "control/super", Description: "Сделать суперадмином /control: control/super <email или id>", Run: func(args console.Args) error {
		if len(args.Positional) == 0 {
			return fmt.Errorf("укажите email или id: control/super you@example.com")
		}
		u, err := control.MakeSuper(args.Positional[0])
		if err != nil {
			return err
		}
		fmt.Printf("Суперадмин: #%d %s <%s> — все разделы /control открыты\n", u.Int64("id"), u.Str("name"), u.Str("email"))
		return nil
	}},
	{Name: "meetups/demo", Description: "Досоздать тестовые события на неделю (AMURA_DEV=1)", Run: func(args console.Args) error {
		fmt.Printf("Добавлено тестовых событий: %d\n", meetups.EnsureDemo())
		return nil
	}},
}
