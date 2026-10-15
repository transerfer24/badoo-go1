// Package photos — фото анкет и селфи для верификации (перенос backend/images.go + storage.go).
//
// Хранилище — modules/files ядра (S3 общего бакета, папка Params["photos.prefix"]; локально — web/uploads).
// Своей реализации S3 (signV4 в storage.go) больше нет. От старого кода остаётся главное: картинка
// перекодируется в JPEG до 1280 px — это убирает EXIF (в том числе GPS из фото) и режет вес.
//
// В таблице photos.file лежит путь в хранилище; адрес для показа — Url(path).
package photos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // декодер PNG
	"io"
	"net/http"
	"path"
	"strings"

	"whales.team/go1/app"
	"whales.team/go1/db"
	"whales.team/go1/modules/files"
	"whales.team/go1/web"
)

const (
	MaxPhotos = 6
	maxUpload = 8 << 20
)

var ErrBadImage = errors.New("Нужен JPG или PNG до 8 МБ")

// Kind — куда класть: фото анкеты (видны всем) или селфи для модерации (только модераторам).
type Kind string

const (
	KindPhoto   Kind = "photos"
	KindPrivate Kind = "private"
)

func prefix() string { p, _ := app.Params["photos.prefix"].(string); return strings.TrimSuffix(p, "/") }

// Url — адрес фото по пути из photos.file (presigned для приватного S3, /uploads/... локально).
func Url(storagePath string) string {
	if storagePath == "" {
		return ""
	}
	return files.Store().Url(storagePath, 0)
}

// Store — принять загруженный файл (поле формы field), перекодировать и положить в хранилище.
// Возвращает путь для photos.file / users.verify_file.
func Store(c *web.Context, field string, kind Kind) (string, error) {
	fh, err := c.Upload(field)
	if err != nil || fh == nil {
		return "", ErrBadImage
	}
	src, err := fh.Open()
	if err != nil {
		return "", ErrBadImage
	}
	defer src.Close()
	return StoreReader(c.Ctx(), src, kind, c.User.Id)
}

// StoreReader — то же из reader (API v1, импорт).
func StoreReader(ctx context.Context, src io.Reader, kind Kind, uid int64) (string, error) {
	maxSide := 1280
	if kind == KindPrivate {
		maxSide = 1024
	}
	data, err := encodeImage(src, maxSide)
	if err != nil {
		return "", err
	}
	f, err := files.Save(ctx, "photo.jpg", bytes.NewReader(data), files.Opts{Images: true, Dir: path.Join(prefix(), string(kind))})
	if err != nil {
		return "", fmt.Errorf("Не удалось сохранить фото, попробуйте ещё раз (%w)", err)
	}
	if uid > 0 {
		_, _ = app.Db.Update("{{file}}", db.H{"user_id": uid}, db.H{"id": f.Id})
	}
	return f.Path, nil
}

// Delete — убрать файл из хранилища по пути (и его запись в file).
func Delete(storagePath string) {
	if storagePath == "" {
		return
	}
	if id := app.Db.CreateCommand("SELECT id FROM {{file}} WHERE path = :p").BindValue(":p", storagePath).MustInt(); id > 0 {
		_ = files.Delete(id)
		return
	}
	_ = files.Store().Delete(context.Background(), storagePath)
}

// encodeImage декодирует картинку, уменьшает до maxSide и перекодирует в JPEG (EXIF/GPS вырезаются).
func encodeImage(src io.Reader, maxSide int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(src, maxUpload+1))
	if err != nil || len(data) > maxUpload {
		return nil, ErrBadImage
	}
	ct := http.DetectContentType(data)
	if ct != "image/jpeg" && ct != "image/png" {
		return nil, ErrBadImage
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width*cfg.Height > 40_000_000 {
		return nil, ErrBadImage
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrBadImage
	}
	img = downscale(img, maxSide)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 84}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Уменьшение усреднением блоков пикселей (box filter) — без внешних библиотек.
func downscale(src image.Image, maxSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return src
	}
	scale := float64(maxSide) / float64(max(w, h))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			var r, g, bl, n uint64
			step := max(1, (x1-x0)/4) // не больше ~16 сэмплов на пиксель
			for yy := y0; yy < max(y1, y0+1); yy += step {
				for xx := x0; xx < max(x1, x0+1); xx += step {
					cr, cg, cb, _ := src.At(xx, yy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			dst.Set(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
		}
	}
	return dst
}

// ---------- Фото анкеты (таблица photos) — общее для веба (MeController) и API v1 ----------

type Photo struct {
	Id  int64
	Url string
}

// Mine — фото пользователя по порядку (главное первым).
func Mine(uid int64) []Photo {
	out := []Photo{}
	for _, r := range app.Db.CreateCommand("SELECT id, file FROM {{photos}} WHERE user_id = :uid ORDER BY pos, id").BindValue(":uid", uid).MustAll() {
		out = append(out, Photo{Id: r.Int64("id"), Url: Url(r.Str("file"))})
	}
	return out
}

// Add — записать сохранённый файл последним в галерею.
func Add(uid int64, name string) {
	pos := app.Db.CreateCommand("SELECT COALESCE(MAX(pos), 0) + 1 FROM {{photos}} WHERE user_id = :uid").BindValue(":uid", uid).MustInt()
	app.Db.MustInsert("{{photos}}", db.H{"user_id": uid, "file": name, "pos": pos})
}

// Remove — удалить своё фото вместе с файлом; false — не своё/нет такого.
func Remove(uid, id int64) bool {
	row := app.Db.CreateCommand("SELECT file FROM {{photos}} WHERE id = :id AND user_id = :uid", db.P{":id": id, ":uid": uid}).MustRow()
	if row == nil {
		return false
	}
	app.Db.MustDelete("{{photos}}", db.H{"id": id, "user_id": uid})
	Delete(row.Str("file"))
	return true
}

// SetMain — сделать фото главным (pos 0, остальные сдвигаются).
func SetMain(uid, id int64) bool {
	if n := app.Db.MustUpdate("{{photos}}", db.H{"pos": 0}, db.H{"id": id, "user_id": uid}); n != 1 {
		return false
	}
	app.Db.CreateCommand("UPDATE {{photos}} SET pos = pos + 1 WHERE user_id = :uid AND id <> :id", db.P{":uid": uid, ":id": id}).MustExecute()
	return true
}
