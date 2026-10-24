import { parseServerTiming } from './perf.js';

export class ApiError extends Error {
    constructor(status, message, body) {
        super(message);
        this.status = status;
        this.body = body;
    }
}

async function request(url, opts = {}) {
    const res = await fetch(url, opts);
    if (!res.ok) {
        const text = await res.text();
        let body = null;
        try { body = JSON.parse(text); } catch (e) { body = null; }
        throw new ApiError(res.status, (body && body.error) || text.trim() || res.statusText, body);
    }
    return res;
}

function jsonBody(method, body) {
    return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
}

// Per-photo endpoints are built from api.base so the same editor can drive
// a file still in Unsorted (/admin/api/staged-develop).
export const api = {
    base: '/admin/api/develop',
    async state(id) {
        return (await request(`${api.base}/${id}`, { cache: 'no-store' })).json();
    },
    async save(id, recipe, revision, label) {
        return (await request(`${api.base}/${id}`, jsonBody('PUT', { recipe, revision, label }))).json();
    },
    saveOnUnload(id, recipe, revision, label) {
        fetch(`${api.base}/${id}`, { ...jsonBody('PUT', { recipe, revision, label }), keepalive: true }).catch(() => {});
    },
    async reset(id) {
        return (await request(`${api.base}/${id}`, { method: 'DELETE' })).json();
    },
    async history(id) {
        return (await request(`${api.base}/${id}/history`, { cache: 'no-store' })).json();
    },
    async wbSample(id, recipe, area) {
        return (await request(`${api.base}/${id}/wb-sample`, jsonBody('POST', { recipe, ...area }))).json();
    },
    async preview(id, recipe, { w, h, quality, generation, signal, region }) {
        const q = new URLSearchParams({ w: String(w), h: String(h), quality });
        if (region) q.set('region', [region.x, region.y, region.w, region.h].map((v) => v.toFixed(6)).join(','));
        const res = await request(`${api.base}/${id}/preview?${q}`, { ...jsonBody('POST', { recipe, generation }), signal });
        const reg = res.headers.get('X-Region');
        let parsed = null;
        if (reg) {
            const [x, y, rw, rh] = reg.split(',').map(Number);
            parsed = { x, y, w: rw, h: rh };
        }
        return {
            blob: await res.blob(),
            generation: Number(res.headers.get('X-Generation')),
            quality: res.headers.get('X-Quality') || quality,
            fullW: Number(res.headers.get('X-Full-Width')) || 0,
            fullH: Number(res.headers.get('X-Full-Height')) || 0,
            region: parsed,
            timing: parseServerTiming(res.headers.get('Server-Timing')),
        };
    },
    warm(id, recipe) {
        fetch(`${api.base}/${id}/warm`, { ...jsonBody('POST', { recipe }), keepalive: true }).catch(() => {});
    },
    async auto(id, recipe, options) {
        return (await request(`${api.base}/${id}/auto`, jsonBody('POST', { recipe, options }))).json();
    },
    async lensSearch(q) {
        return (await request(`/admin/api/lens/search?${new URLSearchParams({ q })}`, { cache: 'no-store' })).json();
    },
    async lensMap(exifLens, profile) {
        return (await request('/admin/api/lens-map', jsonBody('PUT', { exif_lens: exifLens, profile }))).json();
    },
    async presets() {
        return (await request('/admin/api/presets', { cache: 'no-store' })).json();
    },
    async savePreset(name, recipe, modules) {
        return (await request('/admin/api/presets', jsonBody('POST', { name, recipe, modules }))).json();
    },
    async deletePreset(id) {
        return (await request(`/admin/api/presets/${id}`, { method: 'DELETE' })).json();
    },
    async exportStart(id, recipe, options) {
        return (await request(`/admin/api/develop/${id}/export`, jsonBody('POST', { recipe, options }))).json();
    },
    async job(jobId) {
        return (await request(`/admin/api/jobs/${jobId}`, { cache: 'no-store' })).json();
    },
};
