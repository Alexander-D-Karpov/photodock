import { api } from './api.js';

const DRAG_SCALE = 0.6;

// Previewer keeps one request in flight, renders the latest recipe; serves both fit and zoom views.
export class Previewer {
    constructor(id, img, callbacks) {
        this.id = id;
        this.img = img;
        this.cb = callbacks;
        this.gen = 0;
        this.latest = null;
        this.inflight = false;
        this.ctrl = null;
        this.url = null;
    }

    request(recipe, { quality = 'accurate', final = true } = {}) {
        this.latest = { recipe: JSON.parse(JSON.stringify(recipe)), quality, final, scale: final ? 1 : DRAG_SCALE };
        if (final && this.ctrl) this.ctrl.abort();
        if (!this.inflight) this.kick();
    }

    cancel() {
        this.latest = null;
        if (this.ctrl) this.ctrl.abort();
    }

    async kick() {
        const job = this.latest;
        if (!job) return;
        this.latest = null;
        this.inflight = true;
        const ctrl = new AbortController();
        this.ctrl = ctrl;
        const gen = ++this.gen;
        const started = performance.now();
        this.cb.onBusy(true);
        try {
            const { w, h } = this.cb.size(job.scale);
            const region = this.cb.region ? this.cb.region() : null;
            const res = await api.preview(this.id, job.recipe, { w, h, quality: job.quality, generation: gen, signal: ctrl.signal, region });
            if (res.generation === gen && !ctrl.signal.aborted) await this.show(res, job.final, started);
        } catch (err) {
            if (err.name !== 'AbortError') this.cb.onError(err);
        } finally {
            if (this.ctrl === ctrl) this.ctrl = null;
            this.inflight = false;
            if (this.latest) this.kick();
            else this.cb.onBusy(false);
        }
    }

    async show(res, final, started) {
        const url = URL.createObjectURL(res.blob);
        const old = this.url;
        this.img.src = url;
        this.url = url;
        try {
            await this.img.decode();
        } catch (e) {
            return;
        }
        if (old) URL.revokeObjectURL(old);
        this.cb.onFrame({ ...res, final, rtt: performance.now() - started });
    }
}
