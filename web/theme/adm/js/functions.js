function isObject(obj) { return Object.prototype.toString.call(obj) === '[object Object]'; }
function trim(text) { return (text || '').replace(/^\s+|\s+$/g, ''); }
function daysInMonth(month, year) { return new Date(year, month, 0).getDate(); }
function formatRepoSelection (repo) { return repo.title; }
function replace_br(str){ return (str.replace(/\\\\\n|\\\\n|\\n/g, '<br>')).replace(/\\\\\\'|\\\\'|\\'/g,"'"); }
function replace_wp(str){ return (str.replace(/\\\\\n|\\\\n|\\n/g, ' ')).replace(/\\\\\\'|\\\\'|\\'/g,"'"); }
function replace_eol(str){ return (str.replace(/\\\\\n|\\\\n/g, "\n")).replace(/\\\\\\'|\\\\'/g, "'"); }
function popupCenter(url, title, w, h) {var left = (screen.width/2)-(w/2), top = (screen.height/2)-(h/2); return window.open(url, title, 'toolbar=no, location=no, directories=no, status=no, menubar=no, scrollbars=no, resizable=no, copyhistory=no, width='+w+', height='+h+', top='+top+', left='+left); }
function strToDot(str, length) { if(str == undefined || length == undefined) return ''; if(str.length <= length) return str; str = str.substring(0, length); var regex = /\s([^\s]*)$/ig; return str.replace(regex, "..."); }
function strToNum(number, titles) { var cases = [2, 0, 1, 1, 1, 2]; return titles[ (number%100>4 && number%100<20)? 2 : cases[(number%10<5)?number%10:5] ]; }
function capitalize(s){ return s[0].toUpperCase() + s.slice(1); }
function htmlTextLength(s){ if(s == undefined || s == '') return 0; var regex = /<[^>]*>/ig; s = s.replace(regex, ""); return s.length > 0 ? s.length : 0; }
function encode_uri_component (str) { return encodeURIComponent(str).replace(/[!'()*]/g, escape); }
function read_more(selector, height){height = height !== undefined ? height : 110; $(selector).readmore({speed: 0, collapsedHeight: height, moreLink: '<a href="#">Подробнее...</a>', lessLink: '<a href="#">Скрыть</a>'
}); return $(selector); }
function is_mobile(){ var window_width = window.innerWidth ? window.innerWidth : $(window).width(); return window_width <= 800; }
function is_browser(s){return navigator.userAgent.search(s) > -1; }
function is_iphone(){ navigator.userAgent.match(/(iPod|iPhone|iPad|safari)/i) }
function truncate_number(n, r) {r = r || 2; var x = parseInt('1'+('0'.repeat(r))); return Math.floor(n * x) / x;}
function is_int(n) { return parseFloat(n) % 1 === 0; }
function row_to_array(row) { return $(row).map(function() { return $(this).text(); }).get(); }

function add_script(url, callback) {
    if (url) {
        var fileref = document.createElement('script');
        var done = false;
        var head = document.getElementsByTagName("head")[0];

        fileref.onload = fileref.onreadystatechange = function () {
            if (!done && (!this.readyState || this.readyState === "loaded" || this.readyState === "complete")) {
                done = true;

                callback();

                // Handle memory leak in IE
                fileref.onload = fileref.onreadystatechange = null;
                if (head && fileref.parentNode) {
                    head.removeChild(fileref);
                }
            }
        };

        fileref.setAttribute("type", "text/javascript");
        fileref.setAttribute("src", url);

        head.appendChild(fileref);
    }
}

function url_get_param(name) {
    name = name.replace(/[\[]/, "\\[").replace(/[\]]/, "\\]");
    var regex = new RegExp("[\\?&]" + name + "=([^&#]*)"),
        results = regex.exec(location.search);
    return results === null ? "" : results[1].replace(/\+/g, " ");
}

function url_set_param(key, value) {
    var searchUrl = location.search, urlValue;
    if (searchUrl.indexOf("?") == "-1") {
        urlValue = '?' + key + '=' + value;
        // history.pushState({state: 1, rand: Math.random()}, '', urlValue);
        history.replaceState( {} , '', urlValue);
    }
    else {
        if (searchUrl.indexOf(key) == "-1") {
            urlValue = searchUrl + '&' + key + '=' + value;
        }
        else {
            var oldValue = url_get_param(key);
            if (searchUrl.indexOf("?" + key + "=") != "-1") {
                urlValue = searchUrl.replace('?' + key + '=' + oldValue, '?' + key + '=' + value);
            }
            else {
                urlValue = searchUrl.replace('&' + key + '=' + oldValue, '&' + key + '=' + value);
            }
        }

        history.replaceState(history.state, '', urlValue);
    }
}

function url_remove_param(key) {
    var urlValue = decodeURI(document.location.href), searchUrl = decodeURI(location.search);

    if (key != "") {
        var oldValue = decodeURI(url_get_param(key));
        var removeVal = key + "=" + oldValue;
        if (searchUrl.indexOf('?' + removeVal + '&') != "-1") {
            urlValue = urlValue.replace('?' + removeVal + '&', '?');
        }
        else if (searchUrl.indexOf('&' + removeVal + '&') != "-1") {
            urlValue = urlValue.replace('&' + removeVal + '&', '&');
        }
        else if (searchUrl.indexOf('?' + removeVal) != "-1") {
            urlValue = urlValue.replace('?' + removeVal, '');
        }
        else if (searchUrl.indexOf('&' + removeVal) != "-1") {
            urlValue = urlValue.replace('&' + removeVal, '');
        }
    }
    else {
        searchUrl = location.search;
        urlValue = urlValue.replace(searchUrl, '');
    }

    history.replaceState(history.state, '', urlValue);
}

function url_get_param_path(name) {
    name = name.replace(/[\[]/, "\\[").replace(/[\]]/, "\\]");
    var regex = new RegExp("[/]" + name + "/([^&#]*)"),
        results = regex.exec(location.href);
    return results === null ? "" : results[1].replace(/\+/g, " ");
}

function url_set_param_path(key, value) {
    var searchUrl = document.location.href, urlValue;

    if (searchUrl.indexOf('/'+key+'/') === -1) {
        urlValue = searchUrl + '/' + key + '/' + value;
    }
    else {
        var oldValue = url_get_param_path(key);
        urlValue = searchUrl.replace('/' + key + '/' + oldValue, '/' + key + '/' + value);
    }
    history.pushState({state: 1, rand: Math.random()}, '', urlValue);
}

function url_remove_param_path(key) {
    var urlValue = document.location.href;

    if (key !== "") {
        var oldValue = decodeURI(url_get_param_path(key));
        var removeVal = '/' + key + "/" + oldValue;
        if (urlValue.indexOf(removeVal) !== -1) {
            urlValue = urlValue.replace(removeVal, '');
        }
    }
    history.pushState({state: 1, rand: Math.random()}, '', urlValue);
}

function url_replace_path(old_val, new_val) {
    var searchUrl = document.location.href, urlValue;

    if (searchUrl.indexOf(old_val) !== -1) {
        urlValue = searchUrl.replace(old_val, new_val);
    }
    history.pushState({state: 1, rand: Math.random()}, '', urlValue);
}

function message_info(t){
    BootstrapDialog.show({
        title: 'Информация',
        message: t,
        animate: false,
        type: BootstrapDialog.TYPE_PRIMARY,
        closable: true,
        draggable: false,
        buttons: [
            {
                label: 'Закрыть',
                action: function (dialogItself) {
                    dialogItself.close();
                }
            }],
        callback: function(result) {}
    });
}

function message_success(message, title, position) {
    title = title || 'Информационное сообщение';
    position = position || 'toast-bottom-right';

    setTimeout(function() {
        toastr.options = {
            closeButton: true,
            progressBar: false,
            showMethod: 'slideDown',
            hideMethod: 'fadeOut',
            positionClass: 'toast-bottom-right',
            timeOut: 5000
        };
        toastr.success(message, title);
    }, 0);
}

function message_error(message, title, position) {
    title = title || 'Ошибка';
    position = position || 'toast-top-right';

    setTimeout(function() {
        toastr.options = {
            closeButton: true,
            progressBar: false,
            showMethod: 'slideDown',
            hideMethod: 'fadeOut',
            positionClass: 'toast-bottom-right',
            timeOut: 5000
        };
        toastr.warning(message, title);
    }, 0);
}

function message_popup(args) {
    var title = args.title || 'Информационное сообщение', message = args.message || '',
        image = args.image || '', position = args.position || '', time_out = args.time_out || 5000;

    if(message === '') return;

    var options = {
        closeButton: true,
        progressBar: false,
        hideMethod: 'fadeOut',
        positionClass: 'toast-bottom-right',
        toastClass: 'speak-notify',
        timeOut: time_out
    };

    switch (position){
        case 'top-right':{
            options.positionClass = 'toast-top-right';
            options.showMethod = 'fadeIn';
            break;
        }
        default:{
            options.positionClass = 'toast-bottom-right';
            options.showMethod = 'slideDown';
        }
    }

    var msg = '';

    if(image !== ''){
        msg += '<img alt="avatar" class="image--sm" src="'+image+'">';
    }

    msg += '<div class="text-block">';
    msg += '<h5>'+title+'</h5>';
    msg += message;
    msg += '</div>';

    setTimeout(function() {
        toastr.options = options;
        toastr.success(msg);
    }, 0);
}

function set_country_select(elem, country, show_all, placeholder, countries) {
    placeholder = placeholder !== undefined && placeholder !== '' ? placeholder : lang_quantum('9_121');
    var country_list = countries !== undefined ? countries : loc_countries;
    var options = '<option value="0" title="'+placeholder+'" '+(show_all === undefined ? 'disabled selected hidden' : '')+'>'+placeholder+'</option>';
    $.each(country_list, function (key, val) {
        options += '<option value="'+val.id+'" title="'+val.name+'" '+(country === val.id ? 'selected' : '')+'>'+val.name+'</option>';
    });
    $(elem).html(options);
}

function select2_city(elem, country, placeholder){
    $(elem).select2({
        placeholder: placeholder,
        ajax: {
            url: "/ajax/cities",
            type: 'POST',
            dataType: 'json',
            delay: 300,
            data: function (params) {
                return {
                    q: params.term,
                    id: $.isNumeric(country) ? country :  $(country).val()
                };
            },
            processResults: function (data, params) {
                if(params.term === undefined){
                    return {'results': []};
                }else{
                    params.page = params.page || 1;
                    return {
                        results: data.cities
                    };
                }
            },
            cache: true
        },
        escapeMarkup: function (markup) { return markup; },
        minimumInputLength: 2,
        templateResult: formatRepoCity,
        templateSelection: formatRepoSelection
    });
}

function formatRepo (repo) {
    if(repo.title){
        var html = '';
        var term = $('.select2-container--open input.select2-search__field').val();
        var pattern = new RegExp('('+term+')', 'i');
        var res = repo.title.replace(pattern, "<b>$1</b>");
        html += '<div class="select2-results-text-option">'+res+'</div>';
        return html;
    }

    if(repo.text) return '<div>'+repo.text+'</div>';
}

function formatRepoCity (repo) {
    if(repo.title){
        var html = '';
        var term = $('.select2-container input').val();
        var pattern = new RegExp('('+term+')', 'i');
        var res = repo.title.replace(pattern, "<b>$1</b>");
        html += '<div class="select2-results-text-option" id="location_city_'+repo.id+'" data-name="'+repo.title+'" data-area="'+repo.area+'" data-region="'+repo.region+'">'+res+'</div>';

        if(repo.region !== undefined && repo.region !== ''){
            html += '<div class="select2-option-info">';

            if(repo.area !== undefined && repo.area !== ''){
                html += repo.area+',<br>';
            }

            html += repo.region+'</div>';
        }

        return html;
    }

    if(repo.text) return '<div>'+repo.text+'</div>';
}

function editableVal(cont, opts) {
    if (!cont) return '';
    if (cont.tagName == 'TEXTAREA') return val(cont);
    var el = cont.firstChild;
    var v = '', str = '';
    var contTag = new RegExp('^(DIV|P|LI|OL|TR|TD|BLOCKQUOTE)$');
    while (el) {
        switch (el.nodeType) {
            case 3:
                // str = el.data.replace(/^\n|\n$/g, ' ').replace(/[\n\xa0]/g, ' ').replace(/[ ]+/g, ' ');
                str = el.data;
                // str = str.replace(/^\n|\n$/g, ' ');
                str = str.replace(/[\n\xa0]/g, ' ');
                // str = str.replace(/[ ]+/g, ' ');
                v += str;
                break;
            case 1:
                str = Emoji.editableVal(el);
                if (el.tagName && el.tagName.match(contTag) && str) {
                    if (str.substr(-1) != '\n') {
                        str += '\n';
                    }

                    var prev = el.previousSibling;
                    while(prev && prev.nodeType == 3 && trim(prev.nodeValue) == '') {
                        prev = prev.previousSibling;
                    }
                    if (prev && !(prev.tagName && prev.tagName.match(contTag))) {
                        str = '\n' + str;
                    }

                } else if (el.tagName == 'IMG') {
                    var code = Emoji.getCode(el);
                    if (code) {
                        if (opts && opts.saveEmoji) {
                            str += Emoji.getEmojiHTML(code);
                        } else {
                            str += Emoji.codeToChr(code);
                        }
                    }
                } else if (el.tagName == 'BR') {
                    str += '\n';
                }
                v += str;
                break;
        }
        el = el.nextSibling;
    }
    var regex = /<br\s*[\/]?>/gi;
    return v.replace(regex, "\n");
}

function pasteText(editable, text) {
    var sel = window.getSelection ? window.getSelection() : false;
    var rCont = false;
    if (sel && sel.rangeCount) {
        var r = sel.getRangeAt(0);
        if (r.commonAncestorContainer) {
            rCont = r.commonAncestorContainer;
        } else {
            rCont = r.parentElement ? r.parentElement() : r.item(0);
        }
    } else {
        rCont = false;
    }
    if (!rCont) {
        Emoji.editableFocus(editable, false, true);
    }
    Emoji.insertHTML(text);

    if (editable.check) editable.check();
}

function set_caret(el) {
    el.focus();
    if (typeof window.getSelection !== "undefined"
        && typeof document.createRange !== "undefined") {
        var range = document.createRange();
        range.selectNodeContents(el);
        range.collapse(false);
        var sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
    } else if (typeof document.body.createTextRange !== "undefined") {
        var textRange = document.body.createTextRange();
        textRange.moveToElementText(el);
        textRange.collapse(false);
        textRange.select();
    }

    if( el.nodeName.toLowerCase() === 'input' ) {
        el.value = el.value;
    }
}

function wait_for(obj, prop, func, self, count) {
    if (obj[prop]) {
        func.apply(self);
    } else {
        count = count || 0;
        if (count < 1000) {
            setTimeout(function () {
                wait_for(obj, prop, func, self, count + 1);
            }, 0);
        }
    }
}

function attach_script(url, callback) {
    setTimeout(function () {
        var newScript = document.createElement('script');
        newScript.type = 'text/javascript';
        newScript.src = url;

        var file = document.querySelector('script[src="'+url+'"]');
        if(file === null){
            wait_for(document, 'body', function () {
                document.getElementsByTagName('HEAD')[0].appendChild(newScript);
            });
        }
    }, 0);
}

function coin_show(elem, amount, force) {

    if(amount !== undefined && amount !== '' && amount !== 0){
        if(amount > 0 && !force) return;

        elem = $(elem);

        var id = parseInt(Math.random() * 10000000), type = amount >= 0 ? 'coin_x' : 'coin_y', action = amount >= 0 ? '+' : '';
        $('body').append('<a class="coin '+type+'" id="coin_'+id+'"><span class="coin_action">'+action+'</span><span>'+amount+'</span></a>');

        var coin = $('#coin_'+id);
        coin.css('top', parseInt(elem.offset().top - coin.height()) );
        coin.css('left', parseInt(elem.offset().left + elem.width()/2 - coin.width()/2));

        coin.animate({opacity: 0, top: '-=50'}, 1500, function() {
            $(this).remove();
        });
    }
}

function rating_to_stars(rating) {

    rating = rating ? parseInt(rating) : 0;
    if (rating < 0 || rating > 5) {
        return '';
    }

    var stars = '';


    for (var i = 1; i <= rating; i++) {
        stars += '<i class="fa fa-star"></i> ';
    }

    for (var y = rating + 1; y <= 5; y++) {
        stars += '<i class="fa fa-star-o"></i> ';
    }

    return stars;
}

var wave_req = null;
function update_coins(amount) {

    if(amount !== undefined && amount !== ''){
        $('.account-coins-amount').html(amount);


        if (wave_req !== null) wave_req.abort();
        setTimeout(function() {
            wave_req = $.ajax({
                url: '/simon/checkwavemodalshow',
                type: 'POST',
                dataType: 'json',
                data: { },
                success: function (results) {
                    $(document).ready(function () {
                        if (results.status === 200) {
                            if (results.show !== undefined && parseInt(results.show) === 1) {
                                get_simon_wave_show();
                            }
                        }
                    });
                },
                complete: function () { wave_req = null; }
            });
        }, 1000);

    }

}

$.fn.setFocus = function() {
    return this.each(function() {
        var el = $(this)[0];

        setTimeout(function() {

            if (el.tagName === "INPUT" || el.tagName === "TEXTAREA") {
                el.focus();
                el.setSelectionRange(el.value.length, el.value.length);
            }

            else if (el.contentEditable === "true") {
                el.focus();

                var range = document.createRange();
                var selection = window.getSelection();

                range.selectNodeContents(el);
                range.collapse(false);
                selection.removeAllRanges();
                selection.addRange(range);
            }
        }, 0);
    });
};

function message_ok(message, title) {
    message_success(message, title || 'Готово');
}