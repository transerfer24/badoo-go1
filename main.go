package main

import (
	"os"

	"badoo/config"
	"badoo/migrations"

	"whales.team/go1"
	_ "whales.team/go1/db/drivers/all" // драйверы: sqlite | postgres | mysql | all (для прода хватит одного)
)

func main() {
	cfg := config.App
	cfg.Migrations = migrations.All()
	go1.New(cfg).Run(os.Args)
}
