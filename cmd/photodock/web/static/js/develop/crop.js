const MIN = 0.02;
const HANDLES = ['nw', 'n', 'ne', 'e', 'se', 's', 'sw', 'w'];

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

function fitAspect(r, handle, ratio, frameAR) {
    let w = r.w;
    let h = w * frameAR / ratio;
    if (handle === 'n' || handle === 's') {
        h = r.h;
        w = h * ratio / frameAR;
    }
    const ax = handle.includes('w') ? r.x + r.w : handle.includes('e') ? r.x : r.x + r.w / 2;
    const ay = handle.includes('n') ? r.y + r.h : handle.includes('s') ? r.y : r.y + r.h / 2;
    const maxW = handle.includes('w') ? ax : handle.includes('e') ? 1 - ax : 2 * Math.min(ax, 1 - ax);
    const maxH = handle.includes('n') ? ay : handle.includes('s') ? 1 - ay : 2 * Math.min(ay, 1 - ay);
    const k = Math.min(1, maxW / w, maxH / h);
    w *= k;
    h *= k;
    const x = handle.includes('w') ? ax - w : handle.includes('e') ? ax : ax - w / 2;
    const y = handle.includes('n') ? ay - h : handle.includes('s') ? ay : ay - h / 2;
    return { x, y, w, h };
}

function moveRect(s, dx, dy) {
    return { ...s, x: clamp(s.x + dx, 0, 1 - s.w), y: clamp(s.y + dy, 0, 1 - s.h) };
}

function resizeRect(s, handle, dx, dy, ratio, frameAR) {
    let x0 = s.x;
    let y0 = s.y;
    let x1 = s.x + s.w;
    let y1 = s.y + s.h;
    if (handle.includes('w')) x0 = clamp(x0 + dx, 0, x1 - MIN);
    if (handle.includes('e')) x1 = clamp(x1 + dx, x0 + MIN, 1);
    if (handle.includes('n')) y0 = clamp(y0 + dy, 0, y1 - MIN);
    if (handle.includes('s')) y1 = clamp(y1 + dy, y0 + MIN, 1);
    const r = { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
    return ratio ? fitAspect(r, handle, ratio, frameAR) : r;
}

export class CropTool {
    constructor(root, image) {
        this.root = root;
        this.image = image;
        this.rect = { x: 0, y: 0, w: 1, h: 1 };
        this.aspect = 'free';
        this.portrait = null;
        this.drag = null;
        this.box = document.createElement('div');
        this.box.className = 'dv-crop-rect';
        HANDLES.forEach((h) => {
            const el = document.createElement('span');
            el.className = `dv-crop-h dv-crop-${h}`;
            el.dataset.handle = h;
            this.box.appendChild(el);
        });
        root.appendChild(this.box);
        this.box.addEventListener('pointerdown', (e) => this.down(e));
        this.box.addEventListener('pointermove', (e) => this.move(e));
        this.box.addEventListener('pointerup', () => { this.drag = null; });
        this.box.addEventListener('pointercancel', () => { this.drag = null; });
    }

    frameAR() {
        const w = this.image.naturalWidth;
        const h = this.image.naturalHeight;
        return w && h ? w / h : 1;
    }

    isPortrait() {
        return this.portrait ?? (this.rect.w * this.frameAR() < this.rect.h);
    }

    ratio() {
        if (this.aspect === 'free') return 0;
        let r;
        if (this.aspect === 'original') {
            r = this.frameAR();
        } else {
            const [a, b] = this.aspect.split(':').map(Number);
            r = a / b;
        }
        const portrait = this.isPortrait();
        if ((r > 1 && portrait) || (r < 1 && !portrait)) r = 1 / r;
        return r;
    }

    start(rect, aspect) {
        this.rect = rect ? { ...rect } : { x: 0, y: 0, w: 1, h: 1 };
        this.aspect = aspect || 'free';
        this.portrait = null;
        this.root.hidden = false;
        this.fit();
        this.render();
    }

    stop() {
        this.root.hidden = true;
        this.drag = null;
    }

    value() {
        return { ...this.rect };
    }

    setAspect(aspect) {
        this.aspect = aspect;
        this.fit();
        this.render();
    }

    swap() {
        if (this.aspect === 'free') return;
        this.portrait = !this.isPortrait();
        this.fit();
        this.render();
    }

    frameChanged() {
        this.fit();
        this.render();
    }

    fit() {
        const r = this.ratio();
        if (r) this.rect = fitAspect(this.rect, '', r, this.frameAR());
    }

    render() {
        const { x, y, w, h } = this.rect;
        Object.assign(this.box.style, {
            left: `${x * 100}%`,
            top: `${y * 100}%`,
            width: `${w * 100}%`,
            height: `${h * 100}%`,
        });
    }

    down(e) {
        e.preventDefault();
        this.box.setPointerCapture(e.pointerId);
        const b = this.root.getBoundingClientRect();
        this.drag = {
            handle: e.target.dataset.handle || 'move',
            sx: e.clientX,
            sy: e.clientY,
            bw: b.width,
            bh: b.height,
            start: { ...this.rect },
        };
    }

    move(e) {
        const d = this.drag;
        if (!d) return;
        const dx = (e.clientX - d.sx) / d.bw;
        const dy = (e.clientY - d.sy) / d.bh;
        this.rect = d.handle === 'move'
            ? moveRect(d.start, dx, dy)
            : resizeRect(d.start, d.handle, dx, dy, this.ratio(), this.frameAR());
        this.render();
    }
}
