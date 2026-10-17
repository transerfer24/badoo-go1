// Package icons — иконки веб-версии из макета dating2 (WowMoneyDesign/designes/dating2/assets/icons).
// Встраиваются в бинарник и вставляются в разметку инлайном: цвет — через currentColor, сетевых запросов нет.
// В шаблоне: {{icon "heart" 20}}; размер по умолчанию задаёт CSS (.ic — 20px).
package icons

import (
	"embed"
	"html/template"
	"strconv"
	"strings"
	"sync"
)

//go:embed svg/*.svg
var files embed.FS

var cache sync.Map // имя → svg без размеров

// Icon — <svg class="ic" width height aria-hidden> по имени файла; неизвестное имя — пустая строка.
func Icon(name string, size ...int) template.HTML {
	v, ok := cache.Load(name)
	if !ok {
		b, err := files.ReadFile("svg/" + name + ".svg")
		if err != nil {
			return ""
		}
		v, _ = cache.LoadOrStore(name, strings.TrimSpace(string(b)))
	}
	attrs := `class="ic" aria-hidden="true" focusable="false"`
	if len(size) > 0 && size[0] > 0 {
		s := strconv.Itoa(size[0])
		attrs = `width="` + s + `" height="` + s + `" ` + attrs
	}
	return template.HTML(strings.Replace(v.(string), "<svg ", "<svg "+attrs+" ", 1))
}
