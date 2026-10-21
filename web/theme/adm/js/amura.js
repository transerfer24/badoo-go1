/* Общие функции страниц админки Amura (/control). Подключаются в лейауте после functions.js. */

var GENDERS = { m: 'Мужчина', f: 'Женщина', x: 'Небинарная' };
var GENDER_ICON = { m: '<span style="color:#3399ff;">♂</span>', f: '<span style="color:#e83e8c;">♀</span>', x: '<span style="color:#9da5b1;">⚧</span>' };

// unix-секунды → «17.10.2026 14:05» (0 — прочерк)
function dt(v, fmt) { return parseInt(v) > 0 ? moment.unix(v).format(fmt || 'DD.MM.YYYY HH:mm') : '—'; }
// «5 минут назад»
function ago(v) { return parseInt(v) > 0 ? '<span title="' + dt(v) + '">' + moment.unix(v).fromNow() + '</span>' : '—'; }
function is_online(v) { return parseInt(v) > 0 && (Date.now() / 1000 - v) < 300; }

// возраст по YYYY-MM-DD
function age_of(b) { if (!b) return ''; var a = moment().diff(moment(b, 'YYYY-MM-DD'), 'years'); return isNaN(a) ? '' : a; }

function esc(s) { return he.encode(s == null ? '' : String(s)); }

// аватар: фото или кружок с буквой
function avatar(url, name, size) {
    size = size || 36;
    if (url) return '<img class="am-ava" src="' + esc(url) + '" style="width:' + size + 'px;height:' + size + 'px;" alt="">';
    var l = esc((name || '?').substr(0, 1).toUpperCase());
    return '<span class="am-ava am-ava-empty" style="width:' + size + 'px;height:' + size + 'px;line-height:' + size + 'px;font-size:' + Math.round(size / 2.4) + 'px;">' + l + '</span>';
}

// ссылка на карточку пользователя (зачёркнуто — заблокирован)
function user_link(id, name, banned) {
    if (!parseInt(id)) return '<i style="color:#768192;">удалён</i>';
    var n = esc($.trim(name || '') || ('#' + id));
    return '<a href="/control/users/view?uid=' + id + '"' + (parseInt(banned) ? ' class="am-banned" title="Заблокирован"' : '') + '>' + n + '</a>';
}

function user_cell(id, name, photo, banned, sub) {
    return '<div class="am-user">' + avatar(photo, name, 34) + '<div>' + user_link(id, name, banned) +
        (sub ? '<div class="am-sub">' + sub + '</div>' : '') + '</div></div>';
}

function pill(text, color) { return '<span class="am-pill am-pill-' + (color || 'grey') + '">' + text + '</span>'; }

// значки статусов анкеты
function user_badges(v) {
    var b = '';
    if (parseInt(v.banned)) b += pill('<i class="fa fa-ban"></i> заблокирован', 'red');
    if (parseInt(v.verified) === 2) b += pill('<i class="fa fa-check"></i> верифицирован', 'green');
    if (parseInt(v.verified) === 1) b += pill('селфи на проверке', 'yellow');
    if (parseInt(v.premium_until) * 1000 > Date.now()) b += pill('<i class="fa fa-star"></i> Premium', 'purple');
    if (parseInt(v.invisible)) b += pill('невидимка', 'grey');
    if (v.onboarded !== undefined && !parseInt(v.onboarded)) b += pill('анкета не заполнена', 'grey');
    if (!parseInt(v.banned) && is_online(v.last_seen)) b += pill('онлайн', 'green');
    return b;
}

function empty_row(cols, text) { return '<tr class="no-result"><td colspan="' + cols + '" style="text-align:center;color:#999;">' + (text || 'Нет записей') + '</td></tr>'; }

// ответ экшена с ошибкой → окно с текстом
function show_errors(r) {
    if (!r) return;
    var e = r.errors || {};
    var msg = e.modal_message || r.message || _.values(e)[0] || 'Ошибка';
    message_info(esc(msg));
}

function post(url, data, ok, btn) {
    if (btn) $(btn).prop('disabled', true);
    return $.ajax({
        url: url, type: 'POST', dataType: 'json', data: data || {},
        success: function (r) { if (r.status === 200) { if (ok) ok(r); } else show_errors(r); },
        error: function (x) { if (x.statusText !== 'abort') message_info('Сервер не ответил (' + x.status + ')'); },
        complete: function () { if (btn) $(btn).prop('disabled', false); }
    });
}

// подтверждение действия; withReason — с полем «Причина»
function confirm_action(opts, cb) {
    var msg = '<div>' + (opts.message || '') + '</div>';
    if (opts.reason) msg += '<div class="form-group mt-3 mb-0"><label>' + (opts.reasonLabel || 'Причина') + '</label>' +
        '<input type="text" class="form-control" id="ca_reason" maxlength="255" placeholder="' + esc(opts.reasonPlaceholder || '') + '"></div>';
    BootstrapDialog.show({
        title: opts.title, message: msg, animate: false, closable: true, nl2br: false,
        type: opts.danger ? BootstrapDialog.TYPE_DANGER : BootstrapDialog.TYPE_PRIMARY,
        buttons: [
            { label: opts.ok || 'Да', cssClass: opts.danger ? 'btn btn-danger' : 'btn btn--primary-1', action: function (d) { cb($.trim($('#ca_reason').val() || '')); d.close(); } },
            { label: 'Отмена', cssClass: 'btn btn--secondary', action: function (d) { d.close(); } }
        ]
    });
}

/*
Список со скролл-подгрузкой (как users.html в StockGo1): list({url, tbody, data: fn → фильтры, row: fn(v) → html,
cols, count: '.selector', onLoad: fn(r)}). list.reload() — с первой страницы.
*/
function scroll_list(o) {
    var page = 1, req = null, ns = 'scroll.' + o.tbody.replace(/[^a-z0-9]/gi, '');
    function at_bottom() {
        var el = $(o.tbody)[0];
        if (!el || !el.lastElementChild) return false;
        return el.lastElementChild.getBoundingClientRect().bottom <= window.innerHeight + 40;
    }
    function load(reset) {
        if (!reset && !at_bottom()) return;
        $(document).unbind(ns);
        page = reset ? 1 : page + 1;
        if (req) req.abort();
        if (reset) $(o.tbody).html(o.grid ? '<div class="am-empty">Загрузка...</div>' : empty_row(o.cols || 20, 'Загрузка...'));
        var d = $.extend({ page: page }, o.data ? o.data() : {});
        req = $.ajax({
            url: o.url, type: 'POST', dataType: 'json', data: d,
            success: function (r) {
                if (r.status !== 200) { show_errors(r); return; }
                if (reset) $(o.tbody).empty();
                if (o.count) $(o.count).html(r.count || 0);
                if (o.onLoad) o.onLoad(r, reset);
                var items = r.items || [];
                if (items.length) {
                    var html = '';
                    $.each(items, function (i, v) { html += o.row(v); });
                    $(o.tbody).append(html);
                    $(document).bind(ns, _.debounce(function () { load(false); }, 60));
                    if (at_bottom()) load(false);
                } else if (reset) {
                    $(o.tbody).html(o.grid ? '<div class="am-empty">Нет записей</div>' : empty_row(o.cols || 20));
                }
            },
            complete: function () { req = null; }
        });
    }
    return { reload: function () { load(true); }, stop: function () { $(document).unbind(ns); } };
}

// значения фильтров формы → объект (input/select с name)
function form_data(sel) {
    var d = {};
    $(sel).find('input[name], select[name]').each(function () {
        var el = $(this);
        if (el.is(':checkbox')) { if (el.is(':checked')) d[el.attr('name')] = el.val(); }
        else d[el.attr('name')] = el.val();
    });
    return d;
}

// выгрузка CSV: обычная отправка формы (файл скачивается, страница остаётся), data — фильтры списка
function export_csv(url, data) {
    var f = $('<form method="POST" style="display:none;"></form>').attr('action', url);
    $.each($.extend({}, data || {}, { _csrf: $('meta[name="csrf-token"]').attr('content') }), function (k, v) {
        if (v !== '' && v != null && k !== 'page') f.append($('<input type="hidden">').attr('name', k).val(v));
    });
    f.appendTo('body').submit().remove();
}

// фильтры ↔ адрес страницы (?status=banned&…): ссылка открывает тот же срез, «назад» работает
function filters_to_url(d) {
    var q = [];
    $.each(d, function (k, v) { if (v !== '' && v != null && k !== 'page') q.push(encodeURIComponent(k) + '=' + encodeURIComponent(v)); });
    history.replaceState(null, '', location.pathname + (q.length ? '?' + q.join('&') : ''));
}

function filters_from_url(sel) {
    var p = new URLSearchParams(location.search);
    $(sel).find('input[name], select[name]').each(function () {
        var n = $(this).attr('name');
        if (!p.has(n)) return;
        if ($(this).is(':checkbox')) $(this).prop('checked', p.get(n) === $(this).val());
        else $(this).val(p.get(n));
    });
}

// маленький столбчатый график без библиотек: [{day, n}] за последние days дней
function bars(el, rows, days, color) {
    var map = {}, max = 1, html = '';
    $.each(rows || [], function (i, r) { map[r.day] = parseInt(r.n) || 0; });
    var list = [];
    for (var i = days - 1; i >= 0; i--) {
        var d = moment().utc().subtract(i, 'days').format('YYYY-MM-DD');
        var n = map[d] || 0;
        list.push({ d: d, n: n });
        if (n > max) max = n;
    }
    $.each(list, function (i, x) {
        var h = Math.max(2, Math.round(x.n / max * 100));
        html += '<div class="am-bar" title="' + moment(x.d).format('DD.MM') + ': ' + x.n + '"><div style="height:' + h + '%;background:' + (color || '#321fdb') + ';"></div></div>';
    });
    $(el).html('<div class="am-bars">' + html + '</div><div class="am-bars-legend"><span>' + moment(list[0].d).format('DD.MM') +
        '</span><span>max ' + max + '</span><span>' + moment(list[list.length - 1].d).format('DD.MM') + '</span></div>');
}

// просмотр фото крупно
$(document).on('click', '.am-zoom', function (e) {
    e.preventDefault();
    var src = $(this).data('src') || $(this).attr('src');
    BootstrapDialog.show({ title: 'Фото', message: '<div style="text-align:center;"><img src="' + esc(src) + '" style="max-width:100%;max-height:75vh;border-radius:8px;"></div>', animate: false, nl2br: false, size: BootstrapDialog.SIZE_WIDE });
});

// подписи действий журнала (control_log.action)
var LOG_LABELS = { ban: pill('блокировка', 'red'), unban: pill('разблокировка', 'green'), verify: pill('верифицирован', 'green'), unverify: pill('верификация снята', 'yellow'),
    premium: pill('Premium', 'purple'), 'premium-off': pill('Premium снят', 'grey'), edit: pill('правка анкеты', 'blue'), 'photo-delete': pill('фото удалено', 'yellow'),
    logout: pill('выход везде', 'grey'), delete: pill('аккаунт удалён', 'red'), 'message-delete': pill('сообщение удалено', 'yellow'),
    'reports-reject': pill('жалобы отклонены', 'grey'), 'report-reopen': pill('жалоба снова открыта', 'grey'), permission: pill('права', 'blue'), 'meetup-delete': pill('событие удалено', 'yellow'), section: pill('раздел', 'blue'),
    'language-add': pill('язык добавлен', 'green'), 'language-edit': pill('язык изменён', 'blue'), 'language-hide': pill('язык скрыт', 'grey'), 'language-show': pill('язык показан', 'green'), 'language-delete': pill('язык удалён', 'yellow'),
    'session-end': pill('сессия завершена', 'grey'), export: pill('выгрузка CSV', 'blue') };
function log_label(a) { return LOG_LABELS[a] || esc(a); }

// Таблицы страницы — в .am-table-wrap (горизонтальная прокрутка внутри карточки на узком экране). Таблицы,
// которые страница дорисовывает позже (сводки, логи), оборачиваются тем же наблюдателем.
function wrap_tables(root) {
    $(root || document).find('.c-main table').addBack('.c-main table').each(function () {
        if (!$(this).parent().hasClass('am-table-wrap')) $(this).wrap('<div class="am-table-wrap"></div>');
    });
}
$(function () {
    wrap_tables();
    var main = document.querySelector('.c-main');
    if (main && window.MutationObserver) new MutationObserver(function (list) {
        list.forEach(function (m) { m.addedNodes.forEach(function (n) { if (n.nodeType === 1 && (n.tagName === 'TABLE' || n.querySelector('table'))) wrap_tables(n); }); });
    }).observe(main, { childList: true, subtree: true });
});
