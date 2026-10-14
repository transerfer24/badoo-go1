// Package config — конфигурация Amura (Badoo) на Go1. Аналог config/web.php в Yii2.
// Секреты и адреса — в .env (не коммитится), см. .env.example.
package config

import (
	"badoo/components/auth"
	"badoo/components/users"
	"badoo/controllers"

	"whales.team/go1"
	"whales.team/go1/config"
	"whales.team/go1/db"
	"whales.team/go1/mail"
	"whales.team/go1/metrics"
	"whales.team/go1/modules/admin"
	"whales.team/go1/modules/files"
	"whales.team/go1/rbac"
	"whales.team/go1/web"
)

// .env читается первым — до инициализации переменных ниже (init() сработал бы позже них).
var _ = config.LoadDotEnv(".env")

// COOKIE_KEY=новый,старый — ротация ключа без разлогина (старым только читаем).
var cookieKey, cookiePrev = web.Keys(config.Env("COOKIE_KEY"))

var App = go1.Config{
	Id:       "badoo",
	Name:     "Amura",
	Language: "ru",
	// MySQL хостинга (dreampro_wbadoo). Таблицы без префикса — схема та же, что была в SQLite-версии,
	// чтобы SQL из старого бэкенда переносился как есть: {{users}}, {{votes}}, {{matches}}…
	Db: db.Config{
		DSN:         config.Env("DB_DSN"),
		TablePrefix: "",
	},
	Web: web.Config{
		Addr:       config.Env("ADDR", ":8080"),
		TrustProxy: config.Env("TRUST_PROXY") == "1", // хостинг проксирует в приложение, реальный IP в X-Real-IP
	},
	// Сессии в зашифрованной cookie — для веб-версии. CSRF включается автоматически.
	Session: web.SessionConfig{Key: cookieKey, PreviousKeys: cookiePrev},
	// Пользователи — своя таблица users (как в старом бэкенде): вход через Neuronet, у мобильного приложения —
	// Bearer-токен из таблицы sessions. Модуль user ядра не нужен: пароли живут в Neuronet.
	Auth: web.AuthConfig{
		Finder:          auth.Finder{},
		LoginUrl:        "/login",
		EnableAutoLogin: true,
		Checker:         rbac.New(nil), // роль admin — для /admin
	},
	Redis: config.Env("REDIS_URL"), // на shared пусто: кеш в памяти, очередь в БД
	Mail: mail.Config{
		Host: config.Env("SMTP_HOST"), Port: config.EnvInt("SMTP_PORT", 587),
		Username: config.Env("SMTP_USER"), Password: config.Env("SMTP_PASS"),
		From: config.Env("MAIL_FROM", "noreply@badoo.whales.team"),
	},
	Metrics:    metrics.Config{Enabled: true, Token: config.Env("METRICS_TOKEN")}, // /metrics (по токену), /healthz, /readyz
	AlertUsers: []int64{1},
	SentryDSN:  config.Env("SENTRY_DSN"),
	Modules: []go1.Module{
		admin.New(admin.Config{Title: "Amura", Role: "admin"}),
		// Фото анкет — общий бакет S3 проектов, папка S3_PREFIX (badoo/prod/); без S3_KEY — локально в web/uploads.
		files.New(files.Config{Storage: photoStorage(), Admin: true}),
	},
	Params: map[string]any{
		// Вход только через Neuronet (как wealthbook.app/signin): почта → пароль / код из письма → регистрация.
		"neuronet.url":     config.Env("NEURONET_URL", "https://neuronet.app/apisignin"),
		"neuronet.service": config.EnvInt("NEURONET_SERVICE", 0),
		"neuronet.secret":  config.Env("NEURONET_SECRET"),
		// Push через Expo (backend/push.go): антиспам раз в 20 с на человека и тип.
		"push.enabled":      config.Env("PUSH", "1") == "1",
		"push.url":          config.Env("EXPO_PUSH_URL", "https://exp.host/--/api/v2/push/send"),
		"push.access_token": config.Env("EXPO_ACCESS_TOKEN"),
		// Режимы: dev — верификация селфи сразу, Premium без оплаты; chat_verify — писать могут только подтверждённые.
		"photos.prefix":     config.Env("S3_PREFIX", "badoo/prod/"), // папка Amura в общем бакете
		"amura.dev":         config.Env("AMURA_DEV", "1") == "1",
		"amura.chat_verify": config.Env("AMURA_CHAT_VERIFY", "0") == "1",
	},
	Controllers: []any{
		&controllers.SiteController{},
		&controllers.AuthController{},
		&controllers.EncountersController{},
		&controllers.MeController{},
		&controllers.PeopleController{},
		&controllers.SocialController{},
	},
	// Маршруты как в старом main.go routes(); API v1 и остальные страницы — по мере переноса (PLAN.md).
	Rules: web.Rules{
		{"", "site/index"},
		{"GET login", "auth/login"},
		{"POST login/<step:[a-z-]+>", "auth/step"},
		{"POST logout", "auth/logout"},
		{"onboarding", "auth/onboarding"},
		{"encounters", "encounters/index"},
		{"GET avatar/<id:\\d+>.svg", "site/avatar"},
		{"me", "me/index"},
		{"POST me/photos", "me/photo-upload"},
		{"POST me/photos/<id:\\d+>/delete", "me/photo-delete"},
		{"POST me/photos/<id:\\d+>/main", "me/photo-main"},
		{"settings", "me/settings"},
		{"POST settings/delete", "me/delete-account"},
		{"GET premium", "me/premium"},
		{"POST premium/activate", "me/premium-activate"},
		{"POST premium/cancel", "me/premium-cancel"},
		{"verify", "me/verify"},
		{"GET api/encounters", "encounters/cards"},
		{"POST api/vote", "encounters/vote"},
		{"POST api/undo", "encounters/undo"},
		{"GET nearby", "people/nearby"},
		{"GET u/<id:\\d+>", "people/profile"},
		{"POST u/<id:\\d+>/vote", "people/vote"},
		{"POST u/<id:\\d+>/block", "people/block"},
		{"POST u/<id:\\d+>/report", "people/report"},
		{"GET likes", "social/likes"},
		{"GET visitors", "social/visitors"},
		{"GET messages", "social/messages"},
		{"GET chat/<id:\\d+>", "social/chat"},
		{"GET api/chat/<id:\\d+>/messages", "social/chat-messages"},
		{"POST api/chat/<id:\\d+>/send", "social/chat-send"},
		{"POST api/chat/<id:\\d+>/read", "social/chat-read"},
		{"GET api/events", "social/events"},
		{"GET api/counts", "social/counts"},
	},
	OnInit: func(a *go1.Application) error {
		// Лейауту нужна анкета текущего пользователя (аватар, Premium, onboarded) — функцией шаблона,
		// один раз на запрос: {{$me := me .Ctx}} (docs/style.md ядра, «Перенос с Yii», п. 2).
		a.Server.Views.AddFunc("me", users.Current)
		a.Server.Views.AddFunc("counts", users.Counts)
		a.Server.Views.AddFunc("gender", func(g string) string { return users.GenderLabels[g] })
		a.Server.Views.AddFunc("intentLabels", func() map[string]string { return users.IntentLabels })
		return nil
	},
}

// photoStorage — S3, если заданы ключи; иначе локальная папка (разработка).
func photoStorage() files.Storage {
	if config.Env("S3_KEY") == "" {
		return files.Local{Dir: "web/uploads", BaseUrl: "/uploads"}
	}
	return files.S3{
		Endpoint: config.Env("S3_ENDPOINT", "https://s3.eu-central-1.amazonaws.com"),
		Region:   config.Env("S3_REGION", "eu-central-1"),
		Bucket:   config.Env("S3_BUCKET"),
		Key:      config.Env("S3_KEY"),
		Secret:   config.Env("S3_SECRET"),
		// папка внутри бакета — в имени файла при сохранении (Params["photos.prefix"]), у files.S3 префикса нет
	}
}
