function decimals(step) {
    return (String(step).split('.')[1] || '').length;
}

export function createSlider(spec, handlers) {
    const row = document.createElement('div');
    row.className = 'dv-slider' + (spec.cls ? ' ' + spec.cls : '');
    row.innerHTML = '<div class="dv-slider-head"><label></label><input class="dv-slider-num" type="number"></div><input class="dv-slider-range" type="range">';
    const label = row.querySelector('label');
    const num = row.querySelector('.dv-slider-num');
    const range = row.querySelector('.dv-slider-range');
    const toPos = spec.toPos || ((v) => v);
    const fromPos = spec.fromPos || ((p) => p);
    const dec = decimals(spec.step);

    label.textContent = spec.label;
    range.min = spec.posMin ?? spec.min;
    range.max = spec.posMax ?? spec.max;
    range.step = spec.posStep ?? spec.step;
    num.min = spec.min;
    num.max = spec.max;
    num.step = spec.step;
    // iOS's decimal keypad has no minus sign.
    num.inputMode = spec.min < 0 ? 'text' : 'decimal';

    const clamp = (v) => Math.min(spec.max, Math.max(spec.min, v));
    const fmt = (v) => Number(v).toFixed(dec);
    let active = false;

    const show = (v) => {
        range.value = toPos(v);
        num.value = fmt(v);
        row.title = spec.format ? spec.format(v) : '';
    };

    range.addEventListener('input', () => {
        const v = clamp(fromPos(Number(range.value)));
        if (!active) {
            active = true;
            handlers.start();
        }
        num.value = fmt(v);
        handlers.input(v);
    });
    range.addEventListener('change', () => {
        const v = clamp(fromPos(Number(range.value)));
        active = false;
        handlers.commit(v);
    });
    num.addEventListener('change', () => {
        const raw = Number(String(num.value).replace(',', '.'));
        if (Number.isNaN(raw)) return;
        const v = clamp(raw);
        handlers.start();
        handlers.commit(v);
        show(v);
    });

    const reset = () => {
        handlers.start();
        handlers.reset();
    };
    range.addEventListener('dblclick', reset);
    label.addEventListener('dblclick', reset);

    return {
        el: row,
        set: show,
        setDisabled(d) {
            range.disabled = d;
            num.disabled = d;
        },
    };
}
