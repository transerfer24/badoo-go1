// Package push — уведомления через Expo Push Service (он сам доставляет в APNs и FCM). Перенос backend/push.go.
//
// Приложение регистрирует токен (POST /api/v1/me/push), а мы шлём пуш, когда событие (сообщение, мэтч, лайк)
// случилось, пока человек не в приложении: у него нет открытого long-poll / SSE (hub.Offline). Когда приложение
// открыто, хватает живой ленты событий — пуш не дублируем.
//
// Вместо своей горутины с каналом — очередь ядра: задача push.send, воркер `./app queue/work` (keepalive держит его
// рядом с web). Повтор при 5xx/429 — силами очереди (backoff). Антиспам (лайки и мэтчи не чаще раза в 20 с) —
// app.Cache.Add.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/queue"
)

const Job = "push.send"

type Msg struct {
	To        string         `json:"to"`
	Title     string         `json:"title,omitempty"`
	Body      string         `json:"body"`
	Data      map[string]any `json:"data,omitempty"`
	Sound     string         `json:"sound,omitempty"`
	ChannelID string         `json:"channelId,omitempty"`
	ThreadID  string         `json:"threadId,omitempty"` // iOS: группировка по чату
	Priority  string         `json:"priority,omitempty"`
}

func enabled() bool { v, _ := app.Params["push.enabled"].(bool); return v }
func url() string   { v, _ := app.Params["push.url"].(string); return v }
func token() string { v, _ := app.Params["push.access_token"].(string); return v }

// Register — обработчик задачи в очереди ядра; вызывается из OnInit (очередь к этому моменту создана).
func Register() {
	if app.Queue != nil {
		app.Queue.Handle(Job, handle)
	}
}

// Notify — событие для человека, которого нет в приложении (hub.Offline).
func Notify(uid int64, typ string, data json.RawMessage) {
	if !enabled() || app.Queue == nil {
		return
	}
	var d struct {
		MatchId int64  `json:"matchId"`
		From    string `json:"from"`
		Name    string `json:"name"`
		Msg     struct {
			Body string `json:"body"`
		} `json:"msg"`
	}
	_ = json.Unmarshal(data, &d)
	mid := strconv.FormatInt(d.MatchId, 10)
	var m Msg
	switch typ {
	case "message":
		m = Msg{Title: d.From, Body: clip(d.Msg.Body, 140), Data: map[string]any{"url": "/chat/" + mid}, ThreadID: "chat-" + mid}
	case "match":
		m = Msg{Title: "Это взаимно!", Body: "Вы понравились друг другу с " + d.Name + ". Напишите первым", Data: map[string]any{"url": "/chat/" + mid}}
	case "like":
		m = Msg{Body: "Кому-то понравилась ваша анкета", Data: map[string]any{"url": "/likes"}}
	default:
		return
	}
	// лайки и мэтчи не чаще раза в 20 с; сообщения — каждое (но в одном треде iOS)
	if typ != "message" {
		if ok, _ := app.Cache.Add(fmt.Sprintf("push.%d.%s", uid, typ), 1, 20*time.Second); !ok {
			return
		}
	}
	tokens := app.Db.CreateCommand("SELECT token FROM {{push_tokens}} WHERE user_id = :uid").BindValue(":uid", uid).MustStrings()
	if len(tokens) == 0 {
		return
	}
	batch := make([]Msg, 0, len(tokens))
	for _, t := range tokens {
		x := m
		x.To, x.Sound, x.ChannelID, x.Priority = t, "default", "default", "high"
		batch = append(batch, x)
	}
	if err := app.Queue.Push(Job, queue.P{"batch": batch}); err != nil {
		app.Log.Warn("push: не удалось поставить в очередь", "err", err)
	}
}

// handle — отправка пачки (до 100 за запрос — так требует Expo).
func handle(ctx context.Context, job *queue.Job) error {
	var in struct {
		Batch []Msg `json:"batch"`
	}
	if err := job.Bind(&in); err != nil || len(in.Batch) == 0 {
		return queue.Fatal(fmt.Errorf("push: пустая задача: %v", err))
	}
	for len(in.Batch) > 0 {
		n := min(100, len(in.Batch))
		if err := send(ctx, in.Batch[:n]); err != nil {
			return err
		}
		in.Batch = in.Batch[n:]
	}
	return nil
}

// Client — HTTP-клиент к Expo; в тестах подменяется.
var Client = &http.Client{Timeout: 15 * time.Second}

func send(ctx context.Context, batch []Msg) error {
	body, _ := json.Marshal(batch)
	req, err := http.NewRequestWithContext(ctx, "POST", url(), bytes.NewReader(body))
	if err != nil {
		return queue.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if t := token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	res, err := Client.Do(req)
	if err != nil {
		return fmt.Errorf("push: Expo недоступен: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 500 || res.StatusCode == 429 {
		return fmt.Errorf("push: Expo ответил %d", res.StatusCode)
	}
	var out struct {
		Data []struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details struct {
				Error string `json:"error"`
			} `json:"details"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	for i, r := range out.Data {
		if i < len(batch) && r.Status == "error" {
			if r.Details.Error == "DeviceNotRegistered" {
				app.Db.MustDelete("{{push_tokens}}", db.H{"token": batch[i].To})
			} else {
				app.Log.Warn("push: ошибка доставки", "error", r.Details.Error, "message", r.Message)
			}
		}
	}
	return nil
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
