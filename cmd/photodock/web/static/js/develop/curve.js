const SIZE = 240;
const HIT = 10;
const IDENTITY = [[0, 0], [1, 1]];

const clamp01 = (v) => Math.min(1, Math.max(0, v));

// monotone mirrors develop.monotoneSpline so the drawn curve matches the render.
export function monotone(points) {
    const n = points.length;
    if (n < 2) return (x) => x;
    const xs = points.map((p) => p[0]);
    const ys = points.map((p) => p[1]);
    const d = [];
    for (let i = 0; i < n - 1; i++) {
        const h = xs[i + 1] - xs[i];
        d.push(h > 0 ? (ys[i + 1] - ys[i]) / h : 0);
    }
    const m = new Array(n).fill(0);
    m[0] = d[0];
    m[n - 1] = d[n - 2];
    for (let i = 1; i < n - 1; i++) m[i] = d[i - 1] * d[i] > 0 ? (d[i - 1] + d[i]) / 2 : 0;
    for (let i = 0; i < n - 1; i++) {
        if (d[i] === 0) {
            m[i] = 0;
            m[i + 1] = 0;
            continue;
        }
        const a = m[i] / d[i];
        const b = m[i + 1] / d[i];
        const s = a * a + b * b;
        if (s > 9) {
            const t = 3 / Math.sqrt(s);
            m[i] = t * a * d[i];
            m[i + 1] = t * b * d[i];
        }
    }
    return (x) => {
        if (x <= xs[0]) return ys[0];
        if (x >= xs[n - 1]) return x <= 1 ? ys[n - 1] : ys[n - 1] + (x - 1);
        let i = 0;
        while (i < n - 2 && x > xs[i + 1]) i++;
        const h = xs[i + 1] - xs[i];
        const t = (x - xs[i]) / h;
        const t2 = t * t;
        const t3 = t2 * t;
        return (2 * t3 - 3 * t2 + 1) * ys[i] + (t3 - 2 * t2 + t) * h * m[i]
            + (-2 * t3 + 3 * t2) * ys[i + 1] + (t3 - t2) * h * m[i + 1];
    };
}

export class CurveEditor {
    constructor({ onStart, onInput, onCommit }) {
        this.cb = { onStart, onInput, onCommit };
        this.points = IDENTITY.map((p) => [...p]);
        this.hist = null;
        this.drag = -1;
        this.removing = false;
        this.disabled = false;
        this.el = document.createElement('div');
        this.el.className = 'dv-curve';
        this.canvas = document.createElement('canvas');
        this.el.appendChild(this.canvas);
        const dpr = window.devicePixelRatio || 1;
        this.canvas.width = SIZE * dpr;
        this.canvas.height = SIZE * dpr;
        this.canvas.style.width = SIZE + 'px';
        this.canvas.style.height = SIZE + 'px';
        this.canvas.addEventListener('pointerdown', (e) => this.down(e));
        this.canvas.addEventListener('pointermove', (e) => this.move(e));
        this.canvas.addEventListener('pointerup', () => this.up());
        this.canvas.addEventListener('pointercancel', () => this.up());
        this.canvas.addEventListener('dblclick', (e) => this.dbl(e));
        this.draw();
    }

    get dragging() {
        return this.drag >= 0;
    }

    set(points) {
        this.points = (points && points.length >= 2 ? points : IDENTITY).map((p) => [p[0], p[1]]);
        this.draw();
    }

    setHistogram(bins) {
        this.hist = bins;
        this.draw();
    }

    setDisabled(d) {
        this.disabled = d;
        this.canvas.style.opacity = d ? '0.4' : '';
    }

    value() {
        const identity = this.points.every((p) => Math.abs(p[0] - p[1]) < 1e-4);
        return identity ? null : this.points.map((p) => [+p[0].toFixed(4), +p[1].toFixed(4)]);
    }

    rawPos(e) {
        const r = this.canvas.getBoundingClientRect();
        return [(e.clientX - r.left) / r.width, 1 - (e.clientY - r.top) / r.height];
    }

    hit(p) {
        for (let i = 0; i < this.points.length; i++) {
            const dx = (this.points[i][0] - p[0]) * SIZE;
            const dy = (this.points[i][1] - p[1]) * SIZE;
            if (Math.hypot(dx, dy) <= HIT) return i;
        }
        return -1;
    }

    down(e) {
        if (this.disabled) return;
        const p = this.rawPos(e).map(clamp01);
        let i = this.hit(p);
        if (i < 0) {
            i = this.points.findIndex((q) => q[0] > p[0]);
            if (i <= 0) return;
            this.points.splice(i, 0, p);
        }
        this.cb.onStart();
        this.drag = i;
        this.canvas.setPointerCapture(e.pointerId);
        this.update(e);
    }

    move(e) {
        if (this.drag < 0) return;
        this.update(e);
    }

    update(e) {
        const raw = this.rawPos(e);
        const i = this.drag;
        const last = this.points.length - 1;
        this.removing = i !== 0 && i !== last && (raw[1] < -0.08 || raw[1] > 1.08 || raw[0] < -0.08 || raw[0] > 1.08);
        const lo = i === 0 ? 0 : this.points[i - 1][0] + 0.01;
        const hi = i === last ? 1 : this.points[i + 1][0] - 0.01;
        this.points[i] = [Math.min(hi, Math.max(lo, raw[0])), clamp01(raw[1])];
        this.draw();
        this.cb.onInput(this.removing ? this.withoutDragged() : this.value());
    }

    withoutDragged() {
        const keep = this.points.filter((_, k) => k !== this.drag);
        const identity = keep.every((p) => Math.abs(p[0] - p[1]) < 1e-4);
        return identity ? null : keep;
    }

    up() {
        if (this.drag < 0) return;
        if (this.removing) this.points.splice(this.drag, 1);
        this.removing = false;
        this.drag = -1;
        this.draw();
        this.cb.onCommit(this.value());
    }

    dbl(e) {
        if (this.disabled) return;
        const i = this.hit(this.rawPos(e).map(clamp01));
        if (i <= 0 || i >= this.points.length - 1) return;
        this.cb.onStart();
        this.points.splice(i, 1);
        this.draw();
        this.cb.onCommit(this.value());
    }

    draw() {
        const ctx = this.canvas.getContext('2d');
        const dpr = window.devicePixelRatio || 1;
        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        ctx.fillStyle = '#0e0e0e';
        ctx.fillRect(0, 0, SIZE, SIZE);

        if (this.hist) {
            let peak = 1;
            for (let i = 2; i < 254; i++) peak = Math.max(peak, this.hist[i]);
            ctx.beginPath();
            ctx.moveTo(0, SIZE);
            for (let i = 0; i < 256; i++) {
                ctx.lineTo(i / 255 * SIZE, SIZE - Math.sqrt(Math.min(this.hist[i], peak) / peak) * SIZE * 0.9);
            }
            ctx.lineTo(SIZE, SIZE);
            ctx.closePath();
            ctx.fillStyle = 'rgba(255,255,255,0.07)';
            ctx.fill();
        }

        ctx.strokeStyle = '#262626';
        ctx.lineWidth = 1;
        for (let i = 1; i < 4; i++) {
            const v = Math.round(i * SIZE / 4) + 0.5;
            ctx.beginPath();
            ctx.moveTo(v, 0);
            ctx.lineTo(v, SIZE);
            ctx.moveTo(0, v);
            ctx.lineTo(SIZE, v);
            ctx.stroke();
        }
        ctx.setLineDash([3, 3]);
        ctx.strokeStyle = '#3a3a3a';
        ctx.beginPath();
        ctx.moveTo(0, SIZE);
        ctx.lineTo(SIZE, 0);
        ctx.stroke();
        ctx.setLineDash([]);

        const pts = this.removing ? this.points.filter((_, k) => k !== this.drag) : this.points;
        const f = monotone(pts);
        ctx.strokeStyle = '#e8e8e8';
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        for (let x = 0; x <= SIZE; x++) {
            const y = (1 - clamp01(f(x / SIZE))) * SIZE;
            if (x === 0) ctx.moveTo(x, y);
            else ctx.lineTo(x, y);
        }
        ctx.stroke();

        this.points.forEach((p, i) => {
            ctx.beginPath();
            ctx.arc(p[0] * SIZE, (1 - p[1]) * SIZE, 4, 0, Math.PI * 2);
            ctx.fillStyle = i === this.drag ? (this.removing ? '#ef4444' : '#3b82f6') : '#e8e8e8';
            ctx.fill();
        });
    }
}
