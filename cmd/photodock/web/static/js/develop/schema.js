const TEMP_MIN = 2000;
const TEMP_MAX = 25000;

export const tempToPos = (t) => Math.round(1000 * Math.log(t / TEMP_MIN) / Math.log(TEMP_MAX / TEMP_MIN));
export const posToTemp = (p) => Math.round(TEMP_MIN * Math.pow(TEMP_MAX / TEMP_MIN, p / 1000) / 10) * 10;

const signed = (v) => (v > 0 ? '+' : '') + Math.round(v);
const ev = (v) => (v > 0 ? '+' : '') + v.toFixed(2) + ' EV';

const pct = (v) => `${Math.round(100 + v)}%`;
const plain = (v) => String(Math.round(v));

export const TABS = [
    { id: 'basic', label: 'Basic' },
    { id: 'color', label: 'Color' },
    { id: 'detail', label: 'Detail' },
    { id: 'geometry', label: 'Geometry' },
    { id: 'masks', label: 'Masks' },
    { id: 'raw', label: 'RAW', rawOnly: true },
];

// Ranges mirror develop.Recipe.Normalize; the server clamps every field
// anyway, so a mismatch here cannot corrupt stored recipes.
export const MODULES = [
    { id: 'wb', tab: 'basic', title: 'White balance', kind: 'wb', paths: ['wb'] },
    {
        id: 'tone', tab: 'basic', title: 'Tone', auto: 'tone',
        paths: ['tone.exposure', 'tone.contrast', 'tone.highlights', 'tone.shadows', 'tone.whites', 'tone.blacks'],
        sliders: [
            { path: 'tone.exposure', label: 'Exposure', min: -5, max: 5, step: 0.01, format: ev },
            { path: 'tone.contrast', label: 'Contrast', min: -100, max: 100, step: 1, format: signed },
            { path: 'tone.highlights', label: 'Highlights', min: -100, max: 100, step: 1, format: signed },
            { path: 'tone.shadows', label: 'Shadows', min: -100, max: 100, step: 1, format: signed },
            { path: 'tone.whites', label: 'Whites', min: -100, max: 100, step: 1, format: signed },
            { path: 'tone.blacks', label: 'Blacks', min: -100, max: 100, step: 1, format: signed },
        ],
    },
    {
        id: 'local', tab: 'basic', title: 'Local contrast',
        paths: ['local.clarity', 'local.contrast', 'local.dehaze'],
        sliders: [
            { path: 'local.clarity', label: 'Clarity', min: -100, max: 100, step: 1, format: signed },
            { path: 'local.contrast', label: 'Local contrast', min: -100, max: 100, step: 1, format: signed },
            { path: 'local.dehaze', label: 'Dehaze', min: -100, max: 100, step: 1, format: signed },
        ],
    },
    { id: 'curve', tab: 'basic', title: 'Tone curve', kind: 'curve', paths: ['tone.curve'] },
    {
        id: 'presence', tab: 'color', title: 'Presence',
        paths: ['color.vibrance', 'color.saturation'],
        sliders: [
            { path: 'color.vibrance', label: 'Vibrance', min: -100, max: 100, step: 1, format: signed },
            { path: 'color.saturation', label: 'Saturation', min: -100, max: 100, step: 1, format: signed },
        ],
    },
    { id: 'hsl', tab: 'color', title: 'HSL', kind: 'hsl', paths: ['color.hsl'] },
    { id: 'grading', tab: 'color', title: 'Color grading', kind: 'grading', paths: ['color.grading'] },
    { id: 'bw', tab: 'color', title: 'Black & white', kind: 'bw', paths: ['color.bw'] },
    {
        id: 'mixer', tab: 'color', title: 'Channel mixer', paths: ['color.mixer'],
        sliders: ['red', 'green', 'blue'].flatMap((out) => ['r', 'g', 'b'].map((src) => ({
            path: `color.mixer.${out}.${src}`,
            label: `${out[0].toUpperCase()}${out.slice(1)} ← ${src.toUpperCase()}`,
            min: -200, max: 200, step: 1,
            format: (v) => `${v > 0 ? '+' : ''}${Math.round(v)}%`,
        }))),
    },
    {
        id: 'sharpen', tab: 'detail', title: 'Sharpening', paths: ['detail.sharpen'],
        hint: 'Judge sharpening and noise at 1:1 — press Z or click the photo.',
        sliders: [
            { path: 'detail.sharpen.amount', label: 'Amount', min: 0, max: 150, step: 1, format: plain },
            { path: 'detail.sharpen.radius', label: 'Radius', min: 0.5, max: 3, step: 0.1, fallback: 1, format: (v) => v.toFixed(1) + ' px' },
            { path: 'detail.sharpen.threshold', label: 'Masking', min: 0, max: 100, step: 1, format: plain },
        ],
    },
    {
        id: 'noise', tab: 'detail', title: 'Noise reduction', paths: ['detail.noise'],
        sliders: [
            { path: 'detail.noise.luma', label: 'Luminance', min: 0, max: 100, step: 1, format: plain },
            { path: 'detail.noise.chroma', label: 'Color', min: 0, max: 100, step: 1, format: plain },
        ],
    },
    { id: 'geometry', tab: 'geometry', title: 'Crop & rotate', kind: 'geometry', paths: ['geometry'] },
    { id: 'lens', tab: 'geometry', title: 'Lens correction', kind: 'lens', paths: ['lens'] },
    { id: 'masks', tab: 'masks', title: 'Masks', kind: 'masks', paths: ['masks'] },
    { id: 'raw', tab: 'raw', title: 'RAW decoding', kind: 'raw', paths: ['raw'], rawOnly: true },
];

export const LENS_PCT = pct;

export const HSL_BANDS = [
    ['red', 'Red', '#e5484d'],
    ['orange', 'Orange', '#f76b15'],
    ['yellow', 'Yellow', '#ffc53d'],
    ['green', 'Green', '#46a758'],
    ['aqua', 'Aqua', '#12a594'],
    ['blue', 'Blue', '#3e63dd'],
    ['purple', 'Purple', '#8e4ec6'],
    ['magenta', 'Magenta', '#d6409f'],
];

export const DEMOSAIC = [
    ['', 'Default'], ['aahd', 'AAHD'], ['ahd', 'AHD'], ['dcb', 'DCB'], ['dht', 'DHT'],
    ['ppg', 'PPG'], ['vng', 'VNG'], ['linear', 'Bilinear'],
];

export const HIGHLIGHTS = [
    ['', 'Default'], ['clip', 'Clip'], ['unclip', 'Unclip'], ['blend', 'Blend'], ['rebuild', 'Rebuild'],
];

export const ASPECTS = [
    ['free', 'Free'], ['original', 'Original'], ['1:1', '1:1'], ['3:2', '3:2'],
    ['4:3', '4:3'], ['5:4', '5:4'], ['16:9', '16:9'],
];
