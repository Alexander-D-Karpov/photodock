const CHANNELS = [
    ['r', 'rgba(220,60,60,0.75)'],
    ['g', 'rgba(60,200,80,0.75)'],
    ['b', 'rgba(70,110,240,0.75)'],
];

export class Histogram {
    constructor(canvas, loEl, hiEl) {
        this.canvas = canvas;
        this.lo = loEl;
        this.hi = hiEl;
        this.work = document.createElement('canvas');
        this.wctx = this.work.getContext('2d', { willReadFrequently: true });
        this.bins = null;
        this.lum = null;
    }

    update(img) {
        if (!img.naturalWidth) return;
        const s = Math.min(1, 360 / Math.max(img.naturalWidth, img.naturalHeight));
        const w = Math.max(1, Math.round(img.naturalWidth * s));
        const h = Math.max(1, Math.round(img.naturalHeight * s));
        this.work.width = w;
        this.work.height = h;
        this.wctx.drawImage(img, 0, 0, w, h);
        const d = this.wctx.getImageData(0, 0, w, h).data;
        const r = new Uint32Array(256);
        const g = new Uint32Array(256);
        const b = new Uint32Array(256);
        const l = new Uint32Array(256);
        let lo = 0;
        let hi = 0;
        for (let i = 0; i < d.length; i += 4) {
            const R = d[i], G = d[i + 1], B = d[i + 2];
            r[R]++;
            g[G]++;
            b[B]++;
            l[Math.round(0.2126 * R + 0.7152 * G + 0.0722 * B)]++;
            if (R >= 254 || G >= 254 || B >= 254) hi++;
            if (R <= 1 && G <= 1 && B <= 1) lo++;
        }
        const n = d.length / 4;
        this.bins = { r, g, b };
        this.lum = l;
        this.lo.classList.toggle('on', lo / n > 0.002);
        this.hi.classList.toggle('on', hi / n > 0.002);
        this.draw();
    }

    draw() {
        const c = this.canvas;
        const dpr = window.devicePixelRatio || 1;
        const cw = c.clientWidth;
        const ch = c.clientHeight;
        if (!cw || !ch) return;
        c.width = Math.round(cw * dpr);
        c.height = Math.round(ch * dpr);
        const ctx = c.getContext('2d');
        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        ctx.clearRect(0, 0, cw, ch);
        if (!this.bins) return;
        let peak = 1;
        for (const [k] of CHANNELS) {
            for (let i = 2; i < 254; i++) peak = Math.max(peak, this.bins[k][i]);
        }
        ctx.globalCompositeOperation = 'lighter';
        for (const [k, color] of CHANNELS) {
            ctx.beginPath();
            ctx.moveTo(0, ch);
            for (let i = 0; i < 256; i++) {
                ctx.lineTo(i / 255 * cw, ch - Math.sqrt(Math.min(this.bins[k][i], peak) / peak) * (ch - 4));
            }
            ctx.lineTo(cw, ch);
            ctx.closePath();
            ctx.fillStyle = color;
            ctx.fill();
        }
        ctx.globalCompositeOperation = 'source-over';
    }

    drawClipMask(img, canvas) {
        const w = img.naturalWidth;
        const h = img.naturalHeight;
        if (!w || !h) return;
        canvas.width = w;
        canvas.height = h;
        const ctx = canvas.getContext('2d', { willReadFrequently: true });
        ctx.drawImage(img, 0, 0);
        const data = ctx.getImageData(0, 0, w, h);
        const d = data.data;
        for (let i = 0; i < d.length; i += 4) {
            const hi = d[i] >= 254 || d[i + 1] >= 254 || d[i + 2] >= 254;
            const lo = d[i] <= 1 && d[i + 1] <= 1 && d[i + 2] <= 1;
            if (hi) {
                d[i] = 255; d[i + 1] = 0; d[i + 2] = 40; d[i + 3] = 210;
            } else if (lo) {
                d[i] = 30; d[i + 1] = 90; d[i + 2] = 255; d[i + 3] = 210;
            } else {
                d[i + 3] = 0;
            }
        }
        ctx.putImageData(data, 0, 0);
    }
}
