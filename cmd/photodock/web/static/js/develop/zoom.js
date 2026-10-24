// ZoomView shows a full-resolution region at one image pixel per device pixel
// and pans it by dragging; a release asks for the newly exposed region.
// The fit preview (backdrop) stays visible, scaled to the same position, so
// the photo never disappears while a region decodes or after a pan.
export class ZoomView {
    constructor(stage, img, backdrop) {
        this.stage = stage;
        this.img = img;
        this.backdrop = backdrop;
        this.active = false;
        this.cx = 0.5;
        this.cy = 0.5;
        this.fullW = 0;
        this.fullH = 0;
        this.region = null;
        this.drag = null;
        this.onPan = null;
        this.onTap = null;
        stage.addEventListener('pointerdown', (e) => this.down(e));
        stage.addEventListener('pointermove', (e) => this.move(e));
        stage.addEventListener('pointerup', (e) => this.up(e));
        stage.addEventListener('pointercancel', () => { this.drag = null; });
    }

    get dpr() {
        return window.devicePixelRatio || 1;
    }

    setFull(w, h) {
        if (w > 0 && h > 0) {
            this.fullW = w;
            this.fullH = h;
        }
    }

    enter(cx, cy) {
        this.active = true;
        this.cx = cx;
        this.cy = cy;
        this.region = null;
        this.stage.classList.add('zoomed');
        this.img.hidden = true;
        this.layout();
    }

    exit() {
        this.active = false;
        this.region = null;
        this.drag = null;
        this.stage.classList.remove('zoomed');
        this.img.hidden = true;
        if (this.backdrop) this.backdrop.style.transform = '';
    }

    span() {
        const r = this.stage.getBoundingClientRect();
        return {
            w: Math.min(1, (r.width * this.dpr) / this.fullW),
            h: Math.min(1, (r.height * this.dpr) / this.fullH),
        };
    }

    clampCenter() {
        const { w, h } = this.span();
        this.cx = Math.min(1 - w / 2, Math.max(w / 2, this.cx));
        this.cy = Math.min(1 - h / 2, Math.max(h / 2, this.cy));
    }

    wanted() {
        this.clampCenter();
        const { w, h } = this.span();
        return { x: this.cx - w / 2, y: this.cy - h / 2, w, h };
    }

    show(region) {
        if (!this.active) return;
        this.region = region || this.wanted();
        this.img.hidden = false;
        this.layout();
    }

    // layoutBackdrop scales the fit preview so the point (cx, cy) sits at the
    // stage centre at 1:1 scale, matching where the sharp region will land.
    layoutBackdrop(r) {
        const b = this.backdrop;
        if (!b || !b.offsetWidth || !this.fullW) return;
        const w = b.offsetWidth;
        const h = b.offsetHeight;
        const k = this.fullW / this.dpr / w;
        const tx = r.width / 2 - b.offsetLeft - k * this.cx * w;
        const ty = r.height / 2 - b.offsetTop - k * this.cy * h;
        b.style.transformOrigin = '0 0';
        b.style.transform = `translate(${tx}px, ${ty}px) scale(${k})`;
    }

    layout() {
        if (!this.active) return;
        const r = this.stage.getBoundingClientRect();
        this.layoutBackdrop(r);
        if (!this.region) return;
        const s = 1 / this.dpr;
        Object.assign(this.img.style, {
            width: `${this.region.w * this.fullW * s}px`,
            height: `${this.region.h * this.fullH * s}px`,
            left: `${r.width / 2 - (this.cx - this.region.x) * this.fullW * s}px`,
            top: `${r.height / 2 - (this.cy - this.region.y) * this.fullH * s}px`,
        });
    }

    point(e) {
        if (!this.region) return null;
        const b = this.img.getBoundingClientRect();
        return {
            x: this.region.x + ((e.clientX - b.left) / b.width) * this.region.w,
            y: this.region.y + ((e.clientY - b.top) / b.height) * this.region.h,
        };
    }

    down(e) {
        if (!this.active || e.button !== 0) return;
        this.drag = { x: e.clientX, y: e.clientY, cx: this.cx, cy: this.cy, moved: false };
        this.stage.setPointerCapture(e.pointerId);
    }

    move(e) {
        const d = this.drag;
        if (!d) return;
        const dx = e.clientX - d.x;
        const dy = e.clientY - d.y;
        if (Math.abs(dx) + Math.abs(dy) > 3) d.moved = true;
        if (!d.moved) return;
        this.cx = d.cx - (dx * this.dpr) / this.fullW;
        this.cy = d.cy - (dy * this.dpr) / this.fullH;
        this.clampCenter();
        this.layout();
    }

    up(e) {
        const d = this.drag;
        this.drag = null;
        if (!d) return;
        if (d.moved) {
            if (this.onPan) this.onPan();
        } else if (this.onTap) {
            this.onTap(e);
        }
    }
}
