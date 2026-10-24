import { clone } from './recipe.js';

// Mask overlay: draws gradient/radial masks with draggable handles over the
// fit preview. Mask coordinates are normalised to the straightened frame
// before crop, mirroring internal/develop/masks.go.

const NS = 'http://www.w3.org/2000/svg';
const LINE_EXT = 4000;
const WEIGHT_SCALE = 1 / 3;
const HANDLE_R = window.matchMedia && window.matchMedia('(pointer: coarse)').matches ? 13 : 7;

export function newMaskId() {
    return Math.random().toString(36).slice(2, 8);
}

export function frameInfo(recipe, boxW, boxH) {
    const c = (recipe.geometry && recipe.geometry.crop) || { x: 0, y: 0, w: 1, h: 1 };
    const frameAR = (boxW / boxH) * (c.h / c.w);
    const fw = 1;
    const fh = 1 / frameAR;
    return { c, fw, fh, long: Math.max(fw, fh), k: boxW / c.w, boxW, boxH };
}

export function toDisplay(f, nx, ny) {
    return [((nx - f.c.x) / f.c.w) * f.boxW, ((ny - f.c.y) / f.c.h) * f.boxH];
}

export function fromDisplay(f, X, Y) {
    return [f.c.x + (X / f.boxW) * f.c.w, f.c.y + (Y / f.boxH) * f.c.h];
}

const smooth = (a, b, x) => {
    const t = Math.min(1, Math.max(0, (x - a) / (b - a)));
    return t * t * (3 - 2 * t);
};

// maskWeight mirrors maskEval.weight in Go.
export function maskWeight(m, f, nx, ny) {
    const fx = nx * f.fw;
    const fy = ny * f.fh;
    let w;
    if (m.type === 'linear') {
        const x0 = (m.x0 || 0) * f.fw;
        const y0 = (m.y0 || 0) * f.fh;
        const dx = (m.x1 || 0) * f.fw - x0;
        const dy = (m.y1 || 0) * f.fh - y0;
        const d2 = dx * dx + dy * dy;
        if (d2 < 1e-12) return 0;
        w = 1 - smooth(0, 1, ((fx - x0) * dx + (fy - y0) * dy) / d2);
    } else {
        const a = ((m.angle || 0) * Math.PI) / 180;
        const dx = fx - (m.cx || 0) * f.fw;
        const dy = fy - (m.cy || 0) * f.fh;
        const lx = (dx * Math.cos(a) + dy * Math.sin(a)) / Math.max((m.rx || 0) * f.long, 1e-6);
        const ly = (-dx * Math.sin(a) + dy * Math.cos(a)) / Math.max((m.ry || 0) * f.long, 1e-6);
        const r = Math.hypot(lx, ly);
        const inner = 1 - (m.feather || 0) / 100;
        w = inner >= 0.999 ? (r <= 1 ? 1 : 0) : 1 - smooth(inner, 1, r);
    }
    return m.invert ? 1 - w : w;
}

function round4(v) {
    return Math.round(v * 1e4) / 1e4;
}

export function rotateMask(m, dir) {
    const p = (x, y) => (dir > 0 ? [1 - y, x] : [y, 1 - x]);
    if (m.type === 'linear') {
        [m.x0, m.y0] = p(m.x0 || 0, m.y0 || 0);
        [m.x1, m.y1] = p(m.x1 || 0, m.y1 || 0);
    } else {
        [m.cx, m.cy] = p(m.cx || 0, m.cy || 0);
        let a = (m.angle || 0) + (dir > 0 ? 90 : -90);
        if (a > 180) a -= 360;
        if (a <= -180) a += 360;
        m.angle = a;
    }
}

export function flipMask(m, axis) {
    const fx = (v) => (axis === 'h' ? 1 - (v || 0) : v || 0);
    const fy = (v) => (axis === 'v' ? 1 - (v || 0) : v || 0);
    if (m.type === 'linear') {
        m.x0 = fx(m.x0);
        m.x1 = fx(m.x1);
        m.y0 = fy(m.y0);
        m.y1 = fy(m.y1);
    } else {
        m.cx = fx(m.cx);
        m.cy = fy(m.cy);
        m.angle = -(m.angle || 0);
    }
}

export class MaskOverlay {
    constructor(svg, canvas, ctrl) {
        this.svg = svg;
        this.canvas = canvas;
        this.ctrl = ctrl;
        this.visible = false;
        this.selected = null;
        this.showWeights = false;
        this.drag = null;
        this.box = null;
        svg.addEventListener('pointerdown', (e) => this.down(e));
        svg.addEventListener('pointermove', (e) => this.move(e));
        svg.addEventListener('pointerup', (e) => this.up(e));
        svg.addEventListener('pointercancel', (e) => this.up(e));
    }

    setVisible(on) {
        if (this.visible === on) return;
        this.visible = on;
        this.svg.hidden = !on;
        this.canvas.hidden = !(on && this.showWeights);
        this.render();
    }

    setSelected(id) {
        this.selected = id;
        this.render();
    }

    toggleWeights() {
        this.showWeights = !this.showWeights;
        this.canvas.hidden = !(this.visible && this.showWeights);
        this.render();
    }

    layout(box) {
        this.box = box;
        for (const el of [this.svg, this.canvas]) {
            Object.assign(el.style, { left: `${box.left}px`, top: `${box.top}px`, width: `${box.width}px`, height: `${box.height}px` });
        }
        this.svg.setAttribute('viewBox', `0 0 ${box.width} ${box.height}`);
        this.render();
    }

    masks() {
        return this.ctrl.getRecipe().masks || [];
    }

    info() {
        return frameInfo(this.ctrl.getRecipe(), this.box.width, this.box.height);
    }

    node(tag, attrs, parent = this.svg) {
        const e = document.createElementNS(NS, tag);
        Object.entries(attrs).forEach(([k, v]) => e.setAttribute(k, v));
        parent.appendChild(e);
        return e;
    }

    handle(x, y, kind, i, cls = '', parent = this.svg) {
        return this.node('circle', { cx: x, cy: y, r: HANDLE_R, class: `dv-mask-handle ${cls}`, 'data-kind': kind, 'data-index': i }, parent);
    }

    render() {
        if (!this.visible || !this.box || !this.box.width) return;
        while (this.svg.firstChild) this.svg.removeChild(this.svg.firstChild);
        const f = this.info();
        this.masks().forEach((m, i) => {
            if (m.id === this.selected) this.drawSelected(m, i, f);
            else this.drawPin(m, i, f);
        });
        if (this.showWeights) this.drawWeights(f);
    }

    drawPin(m, i, f) {
        const [x, y] = m.type === 'linear'
            ? toDisplay(f, ((m.x0 || 0) + (m.x1 || 0)) / 2, ((m.y0 || 0) + (m.y1 || 0)) / 2)
            : toDisplay(f, m.cx || 0, m.cy || 0);
        this.handle(x, y, 'select', i, `dv-mask-pin${m.off ? ' off' : ''}`);
    }

    drawSelected(m, i, f) {
        if (m.type === 'linear') {
            const [x0, y0] = toDisplay(f, m.x0 || 0, m.y0 || 0);
            const [x1, y1] = toDisplay(f, m.x1 || 0, m.y1 || 0);
            const len = Math.hypot(x1 - x0, y1 - y0) || 1;
            const nx = -(y1 - y0) / len;
            const ny = (x1 - x0) / len;
            [[x0, y0, ''], [x1, y1, '6 4']].forEach(([px, py, dash]) => {
                this.node('line', {
                    x1: px - nx * LINE_EXT, y1: py - ny * LINE_EXT,
                    x2: px + nx * LINE_EXT, y2: py + ny * LINE_EXT,
                    class: 'dv-mask-line', 'stroke-dasharray': dash,
                });
            });
            this.node('line', { x1: x0, y1: y0, x2: x1, y2: y1, class: 'dv-mask-axis' });
            this.handle(x0, y0, 'p0', i);
            this.handle(x1, y1, 'p1', i);
            this.handle((x0 + x1) / 2, (y0 + y1) / 2, 'move', i, 'dv-mask-center');
            return;
        }
        const [cx, cy] = toDisplay(f, m.cx || 0, m.cy || 0);
        const rx = (m.rx || 0) * f.long * f.k;
        const ry = (m.ry || 0) * f.long * f.k;
        const g = this.node('g', { transform: `rotate(${m.angle || 0} ${cx} ${cy})` });
        this.node('ellipse', { cx, cy, rx, ry, class: 'dv-mask-line' }, g);
        const inner = 1 - (m.feather || 0) / 100;
        if (inner < 0.999) {
            this.node('ellipse', { cx, cy, rx: rx * inner, ry: ry * inner, class: 'dv-mask-line', 'stroke-dasharray': '6 4' }, g);
        }
        this.node('line', { x1: cx, y1: cy - ry, x2: cx, y2: cy - ry - 24, class: 'dv-mask-axis' }, g);
        this.handle(cx + rx, cy, 'rx', i, '', g);
        this.handle(cx, cy + ry, 'ry', i, '', g);
        this.handle(cx, cy - ry - 24, 'rot', i, 'dv-mask-rot', g);
        this.handle(cx, cy, 'move', i, 'dv-mask-center', g);
    }

    drawWeights(f) {
        const w = Math.max(1, Math.round(this.box.width * WEIGHT_SCALE));
        const h = Math.max(1, Math.round(this.box.height * WEIGHT_SCALE));
        this.canvas.width = w;
        this.canvas.height = h;
        const ctx = this.canvas.getContext('2d');
        const out = ctx.createImageData(w, h);
        const masks = this.masks().filter((m) => !m.off && (!this.selected || m.id === this.selected));
        for (let y = 0; y < h; y++) {
            for (let x = 0; x < w; x++) {
                const [nx, ny] = fromDisplay(f, (x + 0.5) / WEIGHT_SCALE, (y + 0.5) / WEIGHT_SCALE);
                let wt = 0;
                for (const m of masks) wt = Math.max(wt, maskWeight(m, f, nx, ny));
                const o = (y * w + x) * 4;
                out.data[o] = 255;
                out.data[o + 1] = 40;
                out.data[o + 2] = 40;
                out.data[o + 3] = Math.round(wt * 150);
            }
        }
        ctx.putImageData(out, 0, 0);
    }

    local(e) {
        const r = this.svg.getBoundingClientRect();
        return [e.clientX - r.left, e.clientY - r.top];
    }

    down(e) {
        const t = e.target;
        if (!t.dataset || !t.dataset.kind) return;
        e.preventDefault();
        e.stopPropagation();
        const i = Number(t.dataset.index);
        const m = this.masks()[i];
        if (!m) return;
        if (t.dataset.kind === 'select') {
            this.ctrl.select(m.id);
            return;
        }
        this.svg.setPointerCapture(e.pointerId);
        this.drag = { kind: t.dataset.kind, i, start: clone(m), p: this.local(e), f: this.info(), moved: false };
        this.ctrl.begin(t.dataset.kind === 'move' ? 'Move mask' : 'Edit mask');
    }

    move(e) {
        const d = this.drag;
        if (!d) return;
        d.moved = true;
        const next = this.apply(d, this.local(e));
        this.ctrl.change((r) => { r.masks[d.i] = next; }, {});
        this.render();
    }

    up(e) {
        const d = this.drag;
        if (!d) return;
        this.drag = null;
        const next = d.moved ? this.apply(d, this.local(e)) : d.start;
        this.ctrl.change((r) => { r.masks[d.i] = next; }, { final: true, label: d.kind === 'move' ? 'Move mask' : 'Edit mask' });
    }

    apply(d, [X, Y]) {
        const f = d.f;
        const m = clone(d.start);
        const dxp = X - d.p[0];
        const dyp = Y - d.p[1];
        const shift = (nx, ny) => {
            const [ax, ay] = toDisplay(f, nx, ny);
            return fromDisplay(f, ax + dxp, ay + dyp);
        };
        if (m.type === 'linear') {
            if (d.kind === 'move' || d.kind === 'p0') [m.x0, m.y0] = shift(d.start.x0 || 0, d.start.y0 || 0);
            if (d.kind === 'move' || d.kind === 'p1') [m.x1, m.y1] = shift(d.start.x1 || 0, d.start.y1 || 0);
        } else {
            const [cx, cy] = toDisplay(f, d.start.cx || 0, d.start.cy || 0);
            const a = ((d.start.angle || 0) * Math.PI) / 180;
            const vx = X - cx;
            const vy = Y - cy;
            const lx = vx * Math.cos(a) + vy * Math.sin(a);
            const ly = -vx * Math.sin(a) + vy * Math.cos(a);
            if (d.kind === 'move') [m.cx, m.cy] = shift(d.start.cx || 0, d.start.cy || 0);
            if (d.kind === 'rx') m.rx = Math.max(0.005, Math.abs(lx) / (f.long * f.k));
            if (d.kind === 'ry') m.ry = Math.max(0.005, Math.abs(ly) / (f.long * f.k));
            if (d.kind === 'rot') {
                let deg = (Math.atan2(vy, vx) * 180) / Math.PI + 90;
                if (deg > 180) deg -= 360;
                m.angle = Math.round(deg * 10) / 10;
            }
        }
        ['x0', 'y0', 'x1', 'y1', 'cx', 'cy', 'rx', 'ry'].forEach((k) => {
            if (m[k] !== undefined) m[k] = round4(m[k]);
        });
        return m;
    }
}
