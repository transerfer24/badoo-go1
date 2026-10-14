package main

import (
	"os"

	_ "badoo/commands" // cron-задачи и консольные команды проекта
	"badoo/config"
	"badoo/migrations"

	"whales.team/go1"
	_ "whales.team/go1/db/drivers/mysql" // база проекта — MySQL хостинга; для локальной SQLite временно сменить на drivers/all
)

func main() {
	cfg := config.App
	cfg.Migrations = migrations.All()
	go1.New(cfg).Run(os.Args)
}
