export function clone(v) {
    return v === undefined ? undefined : JSON.parse(JSON.stringify(v));
}

export function getPath(obj, path) {
    return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

export function setPath(obj, path, value) {
    const keys = path.split('.');
    let o = obj;
    for (let i = 0; i < keys.length - 1; i++) {
        if (o[keys[i]] == null || typeof o[keys[i]] !== 'object') o[keys[i]] = {};
        o = o[keys[i]];
    }
    const last = keys[keys.length - 1];
    if (value === undefined || value === null || value === 0 || value === false || value === '') delete o[last];
    else o[last] = value;
}

function prune(v) {
    if (Array.isArray(v)) return v.length ? v : undefined;
    if (v && typeof v === 'object') {
        const out = {};
        for (const k of Object.keys(v).sort()) {
            const p = prune(v[k]);
            if (p !== undefined) out[k] = p;
        }
        return Object.keys(out).length ? out : undefined;
    }
    if (v === 0 || v === false || v === '' || v === null) return undefined;
    return v;
}

export function canonical(v) {
    return JSON.stringify(prune(v) ?? {});
}

export function replaceRecipe(target, next) {
    Object.keys(target).forEach((k) => delete target[k]);
    Object.assign(target, clone(next) || {});
}

// Module ids must match modulePaths in internal/develop/modules.go.
export const MODULE_GROUPS = [
    ['wb', 'White balance', ['wb']],
    ['exposure', 'Exposure', ['tone.exposure']],
    ['tone', 'Tone (contrast, highlights, shadows, whites, blacks)', ['tone.contrast', 'tone.highlights', 'tone.shadows', 'tone.whites', 'tone.blacks']],
    ['curve', 'Tone curve', ['tone.curve']],
    ['local', 'Local contrast', ['local']],
    ['presence', 'Vibrance & saturation', ['color.vibrance', 'color.saturation']],
    ['hsl', 'HSL', ['color.hsl']],
    ['grading', 'Color grading', ['color.grading']],
    ['bw', 'Black & white', ['color.bw']],
    ['mixer', 'Channel mixer', ['color.mixer']],
    ['sharpen', 'Sharpening', ['detail.sharpen']],
    ['noise', 'Noise reduction', ['detail.noise']],
    ['lens', 'Lens correction', ['lens']],
    ['raw', 'RAW decoding', ['raw']],
    ['crop', 'Crop & rotate', ['geometry']],
    ['masks', 'Masks', ['masks']],
];

// Crop and masks are tied to one frame, so they are not pasted by default.
export const DEFAULT_PASTE = MODULE_GROUPS.map((g) => g[0]).filter((id) => id !== 'crop' && id !== 'masks');

export function mergeModules(dst, src, modules) {
    const out = clone(dst) || {};
    MODULE_GROUPS.filter(([id]) => modules.includes(id)).forEach(([, , paths]) => {
        paths.forEach((p) => setPath(out, p, clone(getPath(src, p))));
    });
    return out;
}
