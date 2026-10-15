package meetups

// Тестовые события (только AMURA_DEV=1): на ближайшую неделю набор встреч в городах, где есть анкеты,
// со случайными «идущими» из того же города. Запускается cron-задачей раз в 6 часов (commands/meetups.go)
// и командой `./app meetups/demo`.

import (
	"log/slog"
	"math/rand/v2"
	"time"

	"badoo/components/users"

	"whales.team/go1/app"
	"whales.team/go1/db"
)

type demo struct {
	kind, title, about, place string
	hour, min                 int
	hours                     float64
	price                     string
	dLat, dLng                float64
}

var demos = map[string][]demo{
	"Торревьеха": {
		{"beach", "Пляжный волейбол на закате", "Играем 4×4, уровень любой. Мяч есть, берите воду.", "Пляж Лос-Локос, сетки у спасателей", 18, 30, 2, "", 0.004, 0.012},
		{"language", "Intercambio: испанский ↔ русский/английский", "Полчаса на одном языке, полчаса на другом. Подходит любой уровень.", "Кафе на набережной Хуана Апарисио", 19, 30, 2, "напиток за свой счёт", -0.002, 0.004},
		{"walk", "Прогулка вокруг Розовой лагуны", "Неспешно 6 км, фото фламинго, в конце — горчата.", "Вход в Parque Natural de las Lagunas", 10, 0, 2.5, "", 0.03, -0.01},
		{"drinks", "Пятничные тапас-кросс", "Три бара за вечер, знакомимся по пути.", "Площадь Конституции", 20, 30, 3, "≈15 €", 0.0, 0.0},
		{"sport", "Утренний забег по набережной", "5 км в спокойном темпе, потом кофе.", "Пасео Ла-Мата, у маяка", 8, 30, 1.5, "", 0.025, 0.02},
		{"games", "Квиз в английском пабе", "Команды по 4–6 человек, собираем на месте.", "Центр, улица Ramón Gallud", 21, 0, 2, "5 € с команды", 0.001, -0.002},
		{"food", "Паэлья на террасе", "Готовим вместе с поваром, потом едим.", "Порт, Marina Salinas", 13, 30, 3, "20 €", -0.006, 0.006},
		{"culture", "Вечер фламенко", "Живое выступление, столики у сцены — для нашей группы.", "Teatro Municipal", 21, 30, 2, "12 €", 0.003, -0.004},
	},
	"Валенсия": {
		{"sport", "Велопрогулка по Турии", "12 км по парку, темп спокойный.", "Сады Турии, мост Calatrava", 10, 0, 2, "", 0.0, 0.0},
		{"language", "Intercambio в Русафе", "Испанский, английский, русский — столы по языкам.", "Русафа, кафе на Calle Sueca", 19, 30, 2, "", -0.008, 0.002},
		{"beach", "Пикник на Мальварросе", "Берите плед и что-нибудь к общему столу.", "Пляж Мальварроса", 17, 30, 3, "", 0.0, 0.03},
		{"drinks", "Вечер в Кармен", "Начинаем на террасе, дальше по настроению.", "Barrio del Carmen, Plaza del Tossal", 21, 0, 3, "", 0.006, -0.004},
	},
}

// EnsureDemo — досоздать тестовые события на неделю, убрать прошедшие старше недели. Возвращает число добавленных.
func EnsureDemo() int {
	if !users.DevMode() {
		return 0
	}
	now := time.Now().UTC()
	upcoming := app.Db.CreateCommand("SELECT COUNT(*) FROM {{meetups}} WHERE demo = 1 AND starts_at > :t", db.P{":t": now.Add(48 * time.Hour)}).MustInt()
	if upcoming >= 6 {
		return 0
	}
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		loc = time.FixedZone("CET", 2*3600)
	}
	rng := rand.New(rand.NewPCG(uint64(now.Unix()), 7))
	added := 0
	for city, list := range demos {
		c, ok := users.CityByName(city)
		if !ok {
			continue
		}
		people := app.Db.CreateCommand("SELECT id FROM {{users}} WHERE onboarded = 1 AND city = :city", db.P{":city": city}).MustInts()
		if len(people) < 5 {
			continue
		}
		today := time.Now().In(loc)
		for day := 0; day < 7; day++ {
			for j := 0; j < 1+(day%2); j++ { // 1–2 события в день, по кругу из списка
				d := list[(day*2+j)%len(list)]
				date := today.AddDate(0, 0, day)
				start := time.Date(date.Year(), date.Month(), date.Day(), d.hour, d.min, 0, 0, loc).UTC()
				if start.Before(now) {
					continue
				}
				if app.Db.CreateCommand("SELECT COUNT(*) FROM {{meetups}} WHERE demo = 1 AND title = :t AND starts_at = :s", db.P{":t": d.title, ":s": start}).MustInt() > 0 {
					continue
				}
				id := app.Db.MustInsert("{{meetups}}", db.H{
					"title": d.title, "about": d.about, "kind": d.kind, "place": d.place, "address": "", "city": city,
					"lat": c.Lat + d.dLat, "lng": c.Lng + d.dLng, "starts_at": start, "ends_at": start.Add(time.Duration(d.hours * float64(time.Hour))),
					"capacity": 0, "price": d.price, "host_id": nil, "demo": true, "created_at": now,
				})
				n := 4 + rng.IntN(min(14, len(people)-3))
				for _, k := range rng.Perm(len(people))[:n] {
					going(id, people[k], now)
				}
				added++
			}
		}
	}
	// прошедшие тестовые события старше недели — убираем
	for _, id := range app.Db.CreateCommand("SELECT id FROM {{meetups}} WHERE demo = 1 AND ends_at < :t", db.P{":t": now.Add(-7 * 24 * time.Hour)}).MustInts() {
		app.Db.MustDelete("{{meetup_going}}", db.H{"meetup_id": id})
		app.Db.MustDelete("{{meetups}}", db.H{"id": id})
	}
	if added > 0 {
		slog.Info("события: добавлено тестовых", "n", added)
	}
	return added
}
