package control

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"badoo/migrations"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/web"
)

// startedAt — время запуска процесса (для «работает с …» в «Состоянии системы»)
var startedAt = time.Now()

/*
SystemController — состояние системы (только чтение): сборка, процесс, база, миграции, очередь, какие
интеграции включены (только да/нет — значения ключей сюда не попадают) и хвост логов.

Логи — только суперадмину: в них почты и IP. Файлы — logs/<web|worker|cron>.log рядом с бинарником (так их пишет
deploy/keepalive.sh), каталог можно сменить переменной LOG_DIR.
*/
type SystemController struct{ web.Controller }

func (c *SystemController) Behaviors() []web.Behavior { return access("system", "index") }

func (c *SystemController) Init() { c.Layout = "control" }

func (c *SystemController) ActionIndex() error {
	return c.Render("system", web.M{"section": "system", "is_super": IsSuper(c.User.Id)})
}

func (c *SystemController) ActionGetInfo() error {
	res := web.M{"status": 200}

	build := web.M{"go": runtime.Version(), "os": runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				build["revision"] = s.Value[:min(10, len(s.Value))]
			case "vcs.time":
				build["vcs_time"] = s.Value
			case "vcs.modified":
				build["modified"] = s.Value == "true"
			}
		}
		for _, d := range bi.Deps {
			if d.Path == "whales.team/go1" {
				build["core"] = d.Version
				if d.Replace != nil {
					build["core"] = d.Version + " → " + d.Replace.Path
				}
			}
		}
	}
	res["build"] = build

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	res["process"] = web.M{
		"started": startedAt.Unix(), "uptime": int64(time.Since(startedAt).Seconds()), "pid": os.Getpid(),
		"goroutines": runtime.NumGoroutine(), "cpu": runtime.NumCPU(),
		"mem_alloc": ms.Alloc, "mem_sys": ms.Sys, "gc": ms.NumGC,
	}

	// база: отклик, версия, размеры таблиц
	dbInfo := web.M{"driver": app.Db.DriverName()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t0 := time.Now()
	err := app.Db.Ping(ctx)
	cancel()
	dbInfo["ping_ms"] = float64(time.Since(t0).Microseconds()) / 1000
	dbInfo["ok"] = err == nil
	if app.Db.DriverName() == "mysql" {
		dbInfo["version"] = app.Db.CreateCommand("SELECT VERSION()").MustString()
	} else if app.Db.DriverName() == "sqlite" {
		dbInfo["version"] = app.Db.CreateCommand("SELECT sqlite_version()").MustString()
	}
	tables := []web.M{}
	for _, t := range []string{"users", "photos", "votes", "matches", "messages", "visits", "blocks", "reports",
		"sessions", "push_tokens", "auth_flows", "meetups", "meetup_going", "control_log"} {
		tables = append(tables, web.M{"name": t, "rows": app.Db.CreateCommand("SELECT COUNT(*) FROM {{" + t + "}}").MustInt()})
	}
	dbInfo["tables"] = tables
	res["db"] = dbInfo

	// миграции: применённые (последние) и те, что есть в коде, но не применены
	applied := app.Db.CreateCommand("SELECT version, module, apply_time FROM {{migration}} ORDER BY apply_time DESC, version DESC LIMIT 8").MustAll()
	done := map[string]bool{}
	for _, v := range app.Db.CreateCommand("SELECT version FROM {{migration}}").MustAll() {
		done[v.Str("version")] = true
	}
	pending := []string{}
	for _, m := range migrations.All() {
		if !done[m.Version] {
			pending = append(pending, m.Version)
		}
	}
	res["migrations"] = web.M{"applied": applied, "pending": pending}

	// очередь в БД (на shared без Redis): ждут, упали, сделано за сутки, последние ошибки
	if ok, _ := app.Db.Schema().TableExists("{{queue_job}}"); ok {
		day := time.Now().UTC().Add(-24 * time.Hour)
		q := app.Db.CreateCommand(`
			SELECT COALESCE(SUM(CASE WHEN done_at IS NULL AND failed_at IS NULL THEN 1 ELSE 0 END), 0) AS waiting,
			       COALESCE(SUM(CASE WHEN done_at IS NULL AND failed_at IS NULL AND reserved_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS running,
			       COALESCE(SUM(CASE WHEN failed_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS failed,
			       COALESCE(SUM(CASE WHEN failed_at >= :day THEN 1 ELSE 0 END), 0) AS failed_day,
			       COALESCE(SUM(CASE WHEN done_at >= :day THEN 1 ELSE 0 END), 0) AS done_day
			FROM {{queue_job}}`, db.P{":day": day}).MustRow()
		errs := app.Db.CreateCommand(`SELECT name, attempts, error, failed_at FROM {{queue_job}}
			WHERE failed_at IS NOT NULL ORDER BY failed_at DESC LIMIT 10`).MustAll()
		for _, e := range errs {
			if s := e.Str("error"); len([]rune(s)) > 300 {
				e["error"] = string([]rune(s)[:300]) + "…"
			}
		}
		res["queue"] = web.M{"stats": q, "errors": prepare(errs)}
	}

	// интеграции: только «включено / нет»
	set := func(k string) bool { return strings.TrimSpace(os.Getenv(k)) != "" }
	res["config"] = []web.M{
		{"name": "Режим AMURA_DEV (селфи без проверки, Premium без оплаты)", "on": app.Params["amura.dev"] == true, "warn_on": true},
		{"name": "Писать могут только верифицированные (AMURA_CHAT_VERIFY)", "on": app.Params["amura.chat_verify"] == true},
		{"name": "Push-уведомления (PUSH)", "on": app.Params["push.enabled"] == true},
		{"name": "Expo access token", "on": set("EXPO_ACCESS_TOKEN")},
		{"name": "Вход через Neuronet (NEURONET_SECRET)", "on": set("NEURONET_SECRET")},
		{"name": "Фото в S3 (иначе — web/uploads на сервере)", "on": set("S3_KEY")},
		{"name": "Почта SMTP", "on": set("SMTP_HOST")},
		{"name": "Redis (иначе кеш в памяти, очередь в БД)", "on": set("REDIS_URL")},
		{"name": "Sentry", "on": set("SENTRY_DSN")},
		{"name": "Метрики /metrics по токену", "on": set("METRICS_TOKEN")},
		{"name": "Быстрый вход по ссылке (QUICKAUTH_KEY)", "on": set("QUICKAUTH_KEY")},
	}
	return c.Json(res)
}

// логи, которые можно смотреть (имя → файл в LOG_DIR)
var logFiles = map[string]string{"web": "web.log", "worker": "worker.log", "cron": "cron.log"}

// Хвост лога: name — web/worker/cron, lines — сколько строк (до 1000), errors=1 — только ошибки
func (c *SystemController) ActionGetLog() error {
	if !IsSuper(c.User.Id) {
		return c.Json(web.M{"status": 403, "errors": web.M{"modal_message": "Логи доступны только суперадмину"}})
	}
	file, ok := logFiles[c.Post("name")]
	if !ok {
		file = logFiles["web"]
	}
	dir := os.Getenv("LOG_DIR")
	if dir == "" {
		dir = "logs"
	}
	path := filepath.Join(dir, file)
	lines := min(max(c.PostInt("lines", 200), 20), 1000)

	f, err := os.Open(path)
	if err != nil {
		return c.Json(web.M{"status": 200, "lines": []string{}, "missing": true, "path": path})
	}
	defer f.Close()
	st, _ := f.Stat()
	// читаем последние 1 МБ — этого хватает на 1000 строк с запасом
	const chunk = 1 << 20
	off := max(st.Size()-chunk, 0)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return c.Json(web.M{"status": 500, "errors": web.M{"modal_message": "Не удалось прочитать лог"}})
	}
	all := strings.Split(string(bytes.TrimRight(buf, "\n")), "\n")
	if off > 0 && len(all) > 0 {
		all = all[1:] // первая строка обрезана посередине
	}
	if c.Post("errors") == "1" {
		filtered := all[:0]
		for _, l := range all {
			lw := strings.ToLower(l)
			if strings.Contains(lw, "error") || strings.Contains(lw, "panic") || strings.Contains(lw, "fatal") || strings.Contains(lw, "level=warn") {
				filtered = append(filtered, l)
			}
		}
		all = filtered
	}
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return c.Json(web.M{"status": 200, "lines": all, "size": st.Size(), "modified": st.ModTime().Unix(), "path": path})
}
