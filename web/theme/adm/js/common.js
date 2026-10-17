function display_errors(err){
    $('.form-err').remove();
    $.each( err, function(key, val){
        var el = $('#'+key);
        if(el.next().hasClass('select2')){
            el = el.next();
        }

        el.after('<div class="form-err">'+val+'</div>');
    });
}

function hide_errors(err){
    $('.form-err').remove();
    $('.is-invalid').removeClass('is-invalid');
}

function go_to_error(selector){
    var el = $('.form-err').first();
    if(el.length > 0){
        if(selector !== undefined){
            var scroll_elem = el.closest(selector);
            if(scroll_elem.length > 0){

                if(el.closest('.modal').length > 0){
                    $('.modal').animate({
                        scrollTop: scroll_elem.position().top + 100
                    }, 0);
                }else{
                    $('html,body').animate({
                        scrollTop: scroll_elem.offset().top - 65
                    }, 0);
                }
            }
        }
    }
}

function update_tz(dt, force){
    var d = new Date(), tz = (d.getTimezoneOffset()*60)*-1;
    if(tz !== dt || force){
        $.ajax({
            url: '/ajax/settimezone',
            type: 'POST',
            dataType: 'json',
            data: {
                time_zone: tz
            }
        });
    }
}

$(document).ready(function () {

    $('body').on('click', '[data-href]', function(event){
        if(event.target.tagName.toLowerCase() !== 'a' && event.target.tagName.toLowerCase() !== 'img'){
            window.location = $(this).attr('data-href');
        }
    });

    $('body').on('change keyup input click', '.phone_number', function () {
        var elem = $(this);

        if (elem.prop('tagName') === 'DIV'){
            if (elem.html().match(/[^0-9+]/g)) {
                elem.html(elem.html().replace(/[^0-9+]/g, ''));
            }
            if (!elem.html().match(/^\+/)) {
                elem.html('+'+elem.html());
            }
        }else{
            if (this.value.match(/[^0-9+]/g)) {
                this.value = this.value.replace(/[^0-9+]/g, '');
            }
            if (!this.value.match(/^\+/)) {
                this.value = '+'+this.value;
            }
        }
    });

    $('body').on('change keyup input click', '.number-only', function () {
        var elem = $(this), max_value = elem.attr('data-max');

        if (elem.prop("tagName") == 'DIV'){
            if (elem.html().match(/[^0-9.]/g)) {
                elem.html(elem.html().replace(/[^0-9.]/g, ''));
                setCaret(elem[0]);
            }
            if(max_value != undefined){
                max_value = parseInt(max_value);
                if(parseInt(elem.html()) > max_value){
                    elem.html(max_value);
                    setCaret(elem[0]);
                }
            }
        }else{
            if (this.value.match(/[^0-9.]/g)) {
                this.value = this.value.replace(/[^0-9.]/g, '');
            }
            if(max_value != undefined){
                max_value = parseInt(max_value);
                if(parseInt(this.value) > max_value){
                    this.value = max_value;
                }
            }
        }
    });

    $('body').on('change keyup input click', '.english-only', function () {
        var elem = $(this);

        if (elem.prop('tagName') == 'DIV'){
            if (elem.html().match(/[^a-z'-]/g)) {
                elem.html(elem.html().replace(/[^A-Za-z'-]/g, '').toLowerCase());
                set_caret(elem[0]);
            }
        }else{
            if (this.value.match(/[^a-z'-]/g)) {
                this.value = this.value.replace(/[^A-Za-z'-]/g, '').toLowerCase();
            }
        }
    });

    $('body').on('change keyup input click', 'div[contenteditable="true"]', function () {
        var elem = $(this), max_length = elem.attr('data-max-length'), target = elem.attr('data-target');
        if(max_length != undefined && parseInt(max_length) > 0 && target != undefined && target != ''){
            var length = htmlTextLength(editableVal(elem[0]));
            if(length > max_length){
                $('#'+target).addClass('error');
            }else{
                $('#'+target).removeClass('error');
            }
            $('#'+target).html(htmlTextLength(editableVal(elem[0]))+'/'+max_length);
        }
    });

    $('body').on('paste', 'div[contenteditable="true"]', function (event){
        var clipboardData, pastedData;
        event.stopPropagation();
        event.preventDefault();

        clipboardData = event.originalEvent.clipboardData || window.clipboardData;
        pastedData = clipboardData.getData('Text');
        var clipped = pastedData.replace(/(\r\n|\n|\r)/gm, "<br>");
        pasteText($(this), clipped);
    });

});