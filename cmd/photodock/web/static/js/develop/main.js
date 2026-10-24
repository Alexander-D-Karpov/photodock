import { api, ApiError } from './api.js';
import { clone, canonical, getPath, setPath, replaceRecipe, mergeModules, DEFAULT_PASTE } from './recipe.js';
import { Previewer } from './preview.js';
import { Panels } from './panels.js';
import { Histogram } from './histogram.js';
import { CropTool } from './crop.js';
import { ZoomView } from './zoom.js';
import { MaskOverlay, frameInfo, fromDisplay, newMaskId, rotateMask, flipMask } from './masks.js';
import { PerfHud } from './perf.js';
import { chooseModules } from './dialogs.js';
import { PresetsPanel } from './presets.js';
import { setupExport } from './export.js';
import { bindShortcuts } from './shortcuts.js';

const cfg = window.DEVELOP;
// The same editor drives a file still in Unsorted (cfg.staged).
if (cfg.staged) api.base = '/admin/api/staged-develop';
const $ = (id) => document.getElementById(id);

const CLIPBOARD_KEY = 'photodock-develop-clipboard';
const PASTE_MODULES_KEY = 'photodock-develop-paste-modules';
const SAVE_DELAY = 1200;
const POLL_DELAY = 3000;
const UNDO_LIMIT = 200;
const MAX_MASKS = 16;

const app = {
    id: cfg.staged || cfg.id,
    state: null,
    recipe: {},
    revision: 0,
    savedCanon: canonical({}),
    undo: [],
    redo: [],
    gesture: false,
    label: '',
    mode: 'normal',
    clip: false,
    saving: false,
    saveQueued: false,
    saveTimer: 0,
    pollTimer: 0,
    toastTimer: 0,
    sheetTimer: 0,
    stripIndex: -1,
    fullW: 0,
    fullH: 0,
    maskId: null,
    tab: 'basic',
    warmed: false,
};

const els = {
    stage: $('dv-stage'),
    image: $('dv-image'),
    zoomImage: $('dv-zoom'),
    clipmask: $('dv-clipmask'),
    crop: $('dv-crop'),
    quality: $('dv-quality'),
    beforeBadge: $('dv-before-badge'),
    spinner: $('dv-spinner'),
    error: $('dv-error'),
    toast: $('dv-toast'),
    recipeStatus: $('dv-recipe-status'),
    publishStatus: $('dv-publish-status'),
    history: $('dv-history'),
    strip: $('dv-strip'),
    undo: $('dv-undo'),
    redo: $('dv-redo'),
    before: $('dv-before'),
    zoomButton: $('dv-zoom-btn'),
    clipButton: $('dv-clip'),
    autoButton: $('dv-auto'),
    conflict: $('dv-conflict'),
};

const busy = { fit: false, zoom: false };

function setBusy(kind, value) {
    busy[kind] = value;
    els.spinner.hidden = !(busy.fit || busy.zoom);
}

function stageSize(scale) {
    const r = els.stage.getBoundingClientRect();
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const fit = (v) => Math.max(64, Math.min(4096, Math.round(v * dpr * scale)));
    return { w: fit(r.width - 32), h: fit(r.height - 32) };
}

const histogram = new Histogram($('dv-histogram'), $('dv-clip-lo'), $('dv-clip-hi'));
const crop = new CropTool(els.crop, els.image);
const zoom = new ZoomView(els.stage, els.zoomImage);
const perf = new PerfHud($('dv-perf'));
const mobileQuery = window.matchMedia('(max-width: 900px)');

const fitPreviewer = new Previewer(app.id, els.image, {
    size: stageSize,
    onFrame: onFitFrame,
    onBusy: (b) => setBusy('fit', b),
    onError: (err) => showError(`Preview failed: ${err.message}`),
});

const zoomPreviewer = new Previewer(app.id, els.zoomImage, {
    size: () => stageSize(1),
    region: () => zoom.wanted(),
    onFrame: onZoomFrame,
    onBusy: (b) => setBusy('zoom', b),
    onError: (err) => showError(`1:1 preview failed: ${err.message}`),
});

zoom.onPan = () => zoomPreviewer.request(previewRecipe(), { final: true });
zoom.onTap = (e) => {
    if (app.mode === 'picker') {
        const p = zoom.point(e);
        if (p) pickAt(p.x, p.y);
    } else {
        exitZoom();
    }
};

// Must exist before Panels: its constructor reports the initial tab.
const masksOverlay = new MaskOverlay($('dv-masks'), $('dv-maskweights'), {
    getRecipe: () => app.recipe,
    begin: beginEdit,
    change: changeRecipe,
    select: selectMask,
});

const panels = new Panels($('dv-tabs'), $('dv-modules'), {
    begin: beginEdit,
    change: changeRecipe,
    wbPick: () => setMode(app.mode === 'picker' ? 'normal' : 'picker'),
    wbAuto: autoWB,
    auto: (only) => runAuto({ only, wb: false }, only === 'tone' ? 'Auto tone' : 'Auto'),
    rotate,
    flip,
    cropToggle: () => (app.mode === 'crop' ? applyCrop() : enterCrop()),
    cropCancel: cancelCrop,
    aspect: (a) => { if (app.mode === 'crop') crop.setAspect(a); },
    swapAspect: () => crop.swap(),
    lensSearch: async (q) => {
        try {
            return (await api.lensSearch(q)).items || [];
        } catch (e) {
            return [];
        }
    },
    lensConfirm,
    tabChanged: (id) => {
        app.tab = id;
        syncOverlay();
        if (mobileQuery.matches && app.state) setSheet(true);
    },
    maskAdd: addMask,
    maskSelect: selectMask,
    maskDelete: deleteMask,
    maskDuplicate: duplicateMask,
    maskWeights: () => masksOverlay.toggleWeights(),
});

const presets = new PresetsPanel($('dv-presets'), {
    onApply: applyPreset,
    onError: (err) => showError(err.message, false),
});

let exporter = null;

function previewRecipe() {
    if (app.mode === 'before') return { geometry: clone(app.recipe.geometry) || {} };
    if (app.mode === 'crop') {
        const r = clone(app.recipe);
        if (r.geometry) delete r.geometry.crop;
        return r;
    }
    return app.recipe;
}

function sourceAvailable() {
    return !!(app.state && app.state.source && app.state.source.available);
}

function refreshPreview(quality = 'accurate', final = true) {
    if (!sourceAvailable()) return;
    if (zoom.active) zoomPreviewer.request(previewRecipe(), { quality, final });
    else fitPreviewer.request(previewRecipe(), { quality, final });
}

function beginEdit(label) {
    if (app.gesture) return;
    app.undo.push(clone(app.recipe));
    if (app.undo.length > UNDO_LIMIT) app.undo.shift();
    app.redo = [];
    app.gesture = true;
    app.label = label;
    updateUndoButtons();
}

function changeRecipe(mutate, { quality = 'accurate', final = false, label } = {}) {
    if (!app.gesture) beginEdit(label || 'Edit');
    mutate(app.recipe);
    refreshPreview(final ? 'accurate' : quality, final);
    masksOverlay.render();
    if (final) {
        app.gesture = false;
        if (label) app.label = label;
        panels.refresh(app.recipe);
        markDirty();
    }
}

function isDirty() {
    return canonical(app.recipe) !== app.savedCanon;
}

function setRecipeStatus(kind, message) {
    const map = {
        original: ['Original', 'muted'],
        saved: ['Saved', 'good'],
        modified: ['Modified', 'busy'],
        saving: ['Saving…', 'busy'],
        error: ['Save failed', 'bad'],
    };
    const [text, cls] = map[kind];
    els.recipeStatus.textContent = text;
    els.recipeStatus.className = `dv-pill ${cls}`;
    els.recipeStatus.title = message || 'Recipe';
}

function markDirty() {
    clearTimeout(app.saveTimer);
    if (!isDirty()) {
        setRecipeStatus(app.state && app.state.active ? 'saved' : 'original');
        return;
    }
    setRecipeStatus('modified');
    app.saveTimer = setTimeout(save, SAVE_DELAY);
}

async function save() {
    clearTimeout(app.saveTimer);
    if (!sourceAvailable()) return;
    if (app.saving) {
        app.saveQueued = true;
        return;
    }
    const canon = canonical(app.recipe);
    if (canon === app.savedCanon) {
        setRecipeStatus(app.state && app.state.active ? 'saved' : 'original');
        return;
    }
    app.saving = true;
    setRecipeStatus('saving');
    try {
        const st = await api.save(app.id, app.recipe, app.revision, app.label);
        app.revision = st.revision;
        app.savedCanon = canon;
        app.state = st;
        panels.setContext({ lens: st.lens });
        setRecipeStatus(isDirty() ? 'modified' : 'saved');
        updatePublish(st);
        schedulePoll();
        loadHistory();
        markStripEdited(true);
    } catch (err) {
        if (err instanceof ApiError && err.status === 409 && err.body && err.body.state) {
            showConflict(err.body.state);
        } else {
            setRecipeStatus('error', err.message);
        }
    } finally {
        app.saving = false;
        if (app.saveQueued) {
            app.saveQueued = false;
            save();
        }
    }
}

async function flush() {
    clearTimeout(app.saveTimer);
    if (app.mode === 'crop') applyCrop();
    if (isDirty() && !app.saving) await save();
    while (app.saving) await new Promise((r) => setTimeout(r, 50));
}

function showConflict(remote) {
    const close = () => els.conflict.close();
    $('dv-conflict-overwrite').onclick = () => {
        close();
        app.revision = remote.revision;
        save();
    };
    $('dv-conflict-load').onclick = () => {
        close();
        app.undo.push(clone(app.recipe));
        applyState(remote, true);
        refreshPreview();
        loadHistory();
    };
    if (!els.conflict.open) els.conflict.showModal();
}

function updatePublish(st) {
    let text;
    let cls;
    let title = 'Public images';
    if (cfg.staged) {
        text = st.active ? 'Edited · in Unsorted' : 'Unsorted';
        cls = 'muted';
        title = 'Edits are applied when the file is added to the gallery';
    } else if (!st.active) {
        text = 'Original';
        cls = 'muted';
    } else if (st.render_state === 'failed') {
        text = 'Render failed';
        cls = 'bad';
        title = st.render_error || title;
    } else if (st.published_hash && st.published_hash === st.desired_hash) {
        text = 'Published';
        cls = 'good';
    } else {
        text = 'Rendering…';
        cls = 'busy';
    }
    els.publishStatus.textContent = text;
    els.publishStatus.className = `dv-pill ${cls}`;
    els.publishStatus.title = title;
}

function schedulePoll() {
    clearTimeout(app.pollTimer);
    const st = app.state;
    if (cfg.staged || !st || !st.active || st.render_state === 'failed') return;
    if (st.published_hash && st.published_hash === st.desired_hash) return;
    app.pollTimer = setTimeout(async () => {
        try {
            const fresh = await api.state(app.id);
            ['desired_hash', 'published_hash', 'render_state', 'render_error'].forEach((k) => { app.state[k] = fresh[k]; });
            updatePublish(app.state);
        } catch (e) {
            return schedulePoll();
        }
        schedulePoll();
    }, POLL_DELAY);
}

function applyState(st, replace) {
    app.state = st;
    if (replace) {
        app.recipe = clone(st.recipe) || {};
        app.revision = st.revision;
        app.savedCanon = canonical(app.recipe);
        app.gesture = false;
    }
    panels.setContext({
        raw: st.source.kind === 'raw',
        asShot: st.as_shot,
        defaults: st.defaults,
        capabilities: st.capabilities,
        lens: st.lens,
    });
    panels.refresh(app.recipe);
    panels.setDisabled(!st.source.available);
    syncOverlay();
    masksOverlay.render();

    const problems = [];
    if (!st.source.available) problems.push(`Original unavailable: ${st.source.error || 'unknown error'}. Editing is disabled.`);
    (st.warnings || []).forEach((w) => problems.push(w));
    if (problems.length) showError(problems.join(' · '), !st.source.available);
    else hideError();

    setRecipeStatus(isDirty() ? 'modified' : st.active ? 'saved' : 'original');
    updatePublish(st);
    updateUndoButtons();
}

function showError(message, fatal = true) {
    els.error.textContent = message;
    els.error.classList.toggle('warn', !fatal);
    els.error.hidden = false;
}

function hideError() {
    els.error.hidden = true;
}

function toast(message, ms = 1600) {
    els.toast.textContent = message;
    els.toast.classList.add('on');
    clearTimeout(app.toastTimer);
    app.toastTimer = setTimeout(() => els.toast.classList.remove('on'), ms);
}

function updateUndoButtons() {
    els.undo.disabled = !app.undo.length;
    els.redo.disabled = !app.redo.length;
}

function jumpTo(recipe, label) {
    app.recipe = recipe;
    app.gesture = false;
    app.label = label;
    panels.refresh(app.recipe);
    masksOverlay.render();
    refreshPreview();
    markDirty();
    updateUndoButtons();
}

function undo() {
    if (!app.undo.length || app.mode === 'crop') return;
    app.redo.push(clone(app.recipe));
    jumpTo(app.undo.pop(), 'Undo');
}

function redo() {
    if (!app.redo.length || app.mode === 'crop') return;
    app.undo.push(clone(app.recipe));
    jumpTo(app.redo.pop(), 'Redo');
}

function readClipboard() {
    try {
        return JSON.parse(localStorage.getItem(CLIPBOARD_KEY) || 'null');
    } catch (e) {
        return null;
    }
}

function pasteModules() {
    try {
        const v = JSON.parse(localStorage.getItem(PASTE_MODULES_KEY) || 'null');
        if (Array.isArray(v) && v.length) return v;
    } catch (e) {
        return DEFAULT_PASTE;
    }
    return DEFAULT_PASTE;
}

function copySettings() {
    try {
        localStorage.setItem(CLIPBOARD_KEY, JSON.stringify(clone(app.recipe)));
        toast('Settings copied');
    } catch (e) {
        toast('Cannot access local storage');
    }
}

function pasteSettings(modules = pasteModules()) {
    const settings = readClipboard();
    if (!settings) {
        toast('Nothing to paste');
        return;
    }
    beginEdit('Paste settings');
    changeRecipe((r) => replaceRecipe(r, mergeModules(r, settings, modules)), { final: true, label: 'Paste settings' });
    toast('Settings pasted');
}

async function pasteSelective() {
    if (!readClipboard()) {
        toast('Nothing to paste');
        return;
    }
    const res = await chooseModules({ title: 'Paste settings', okLabel: 'Paste', selected: pasteModules() });
    if (!res) return;
    try {
        localStorage.setItem(PASTE_MODULES_KEY, JSON.stringify(res.modules));
    } catch (e) {
        toast('Selection not remembered');
    }
    pasteSettings(res.modules);
}

function applyPreset(p) {
    if (!sourceAvailable()) return;
    beginEdit(`Preset: ${p.name}`);
    changeRecipe((r) => replaceRecipe(r, mergeModules(r, p.recipe, p.modules)), { final: true, label: `Preset: ${p.name}` });
}

async function savePreset() {
    const res = await chooseModules({
        title: 'Save preset',
        okLabel: 'Save',
        withName: true,
        selected: DEFAULT_PASTE.filter((m) => m !== 'lens' && m !== 'raw' && m !== 'wb' && m !== 'exposure'),
    });
    if (!res) return;
    try {
        await api.savePreset(res.name, app.recipe, res.modules);
        presets.load();
        toast(`Preset “${res.name}” saved`);
    } catch (err) {
        showError(`Cannot save preset: ${err.message}`, false);
    }
}

async function resetAll() {
    if (!confirm('Discard all edits and return to the original?')) return;
    clearTimeout(app.saveTimer);
    if (app.mode !== 'normal') setMode('normal');
    try {
        const before = clone(app.recipe);
        const st = await api.reset(app.id);
        app.undo.push(before);
        applyState(st, true);
        refreshPreview();
        loadHistory();
        markStripEdited(false);
    } catch (err) {
        showError(`Reset failed: ${err.message}`);
    }
}

// runAuto asks the server to analyse the frame and merges the proposed
// modules as one undo step.
async function runAuto(options = { wb: true }, label = 'Auto') {
    if (!sourceAvailable() || els.autoButton.disabled) return;
    if (app.mode === 'crop') applyCrop();
    els.autoButton.disabled = true;
    toast('Analysing…', 10000);
    try {
        const res = await api.auto(app.id, app.recipe, options);
        if (!res.modules.length) {
            toast('Nothing to change');
            return;
        }
        beginEdit(label);
        changeRecipe((r) => replaceRecipe(r, mergeModules(r, res.recipe, res.modules)), { final: true, label });
        toast(res.notes.join('\n') || `${label} applied`, 4500);
    } catch (err) {
        toast('');
        showError(`${label} failed: ${err.message}`, false);
    } finally {
        els.autoButton.disabled = false;
    }
}

async function autoWB() {
    await runAuto({ only: 'wb' }, 'Auto white balance');
}

async function pickAt(x, y) {
    setMode('normal');
    try {
        const wb = await api.wbSample(app.id, app.recipe, { x, y, r: zoom.active ? 0.003 : 0.01 });
        beginEdit('White balance picker');
        changeRecipe((rc) => { rc.wb = { mode: 'custom', temp: wb.temp, tint: wb.tint }; }, { final: true, label: 'White balance picker' });
    } catch (err) {
        showError(`White balance picker: ${err.message}`, false);
    }
}

async function lensConfirm(profile) {
    const lens = app.state && app.state.lens;
    if (!lens || !lens.exif_lens) return;
    try {
        await api.lensMap(lens.exif_lens, profile);
        const st = await api.state(app.id);
        applyState(st, false);
        refreshPreview();
        schedulePoll();
        toast(profile ? 'Profile remembered for this lens' : 'Remembered profile removed');
    } catch (err) {
        showError(`Lens mapping failed: ${err.message}`, false);
    }
}

function syncOverlay() {
    masksOverlay.setVisible(app.tab === 'masks' && app.mode !== 'crop' && !zoom.active && sourceAvailable());
}

function selectMask(id) {
    app.maskId = id;
    panels.setMaskSelection(id);
    masksOverlay.setSelected(id);
    panels.refresh(app.recipe);
}

function roundMask(m) {
    ['x0', 'y0', 'x1', 'y1', 'cx', 'cy', 'rx', 'ry'].forEach((k) => {
        if (m[k] !== undefined) m[k] = Math.round(m[k] * 1e4) / 1e4;
    });
    return m;
}

function addMask(type) {
    const box = imageBox();
    if (!box.width || !sourceAvailable()) return;
    if ((app.recipe.masks || []).length >= MAX_MASKS) {
        toast(`At most ${MAX_MASKS} masks`);
        return;
    }
    const f = frameInfo(app.recipe, box.width, box.height);
    const id = newMaskId();
    let m;
    if (type === 'linear') {
        const [x0, y0] = fromDisplay(f, box.width / 2, box.height * 0.1);
        const [x1, y1] = fromDisplay(f, box.width / 2, box.height * 0.5);
        m = { id, type, x0, y0, x1, y1, adjust: { exposure: -0.5 } };
    } else {
        const [cx, cy] = fromDisplay(f, box.width / 2, box.height / 2);
        const r = (Math.min(box.width, box.height) * 0.3) / (f.long * f.k);
        m = { id, type, cx, cy, rx: r, ry: r * 0.75, feather: 50, adjust: { exposure: 0.5 } };
    }
    beginEdit('Add mask');
    changeRecipe((rc) => { rc.masks = [...(rc.masks || []), roundMask(m)]; }, { final: true, label: type === 'linear' ? 'Add gradient' : 'Add radial' });
    selectMask(id);
}

function deleteMask(id) {
    if (!id) return;
    beginEdit('Delete mask');
    changeRecipe((rc) => {
        rc.masks = (rc.masks || []).filter((m) => m.id !== id);
        if (!rc.masks.length) delete rc.masks;
    }, { final: true, label: 'Delete mask' });
    selectMask(null);
}

function duplicateMask(id) {
    const src = (app.recipe.masks || []).find((m) => m.id === id);
    if (!src || app.recipe.masks.length >= MAX_MASKS) return;
    const copy = clone(src);
    copy.id = newMaskId();
    if (copy.type === 'linear') {
        copy.x0 = (copy.x0 || 0) + 0.03;
        copy.x1 = (copy.x1 || 0) + 0.03;
    } else {
        copy.cx = (copy.cx || 0) + 0.03;
        copy.cy = (copy.cy || 0) + 0.03;
    }
    beginEdit('Duplicate mask');
    changeRecipe((rc) => { rc.masks = [...rc.masks, roundMask(copy)]; }, { final: true, label: 'Duplicate mask' });
    selectMask(copy.id);
}

function normRotation(v) {
    return ((v % 360) + 360) % 360;
}

function rotateRect(c, dir) {
    return dir > 0
        ? { x: 1 - c.y - c.h, y: c.x, w: c.h, h: c.w }
        : { x: c.y, y: 1 - c.x - c.w, w: c.h, h: c.w };
}

// rotate mirrors develop.RotateRecipe: crop and masks turn with the frame.
function rotate(dir) {
    if (app.mode === 'crop') applyCrop();
    beginEdit(dir > 0 ? 'Rotate right' : 'Rotate left');
    changeRecipe((r) => {
        const g = r.geometry || (r.geometry = {});
        const flipped = !!g.flip_h !== !!g.flip_v;
        const next = normRotation((g.rotate || 0) + (flipped ? -dir : dir));
        if (next) g.rotate = next;
        else delete g.rotate;
        if (g.crop) g.crop = rotateRect(g.crop, dir);
        (r.masks || []).forEach((m) => {
            rotateMask(m, dir);
            roundMask(m);
        });
    }, { final: true });
}

function flip(axis) {
    if (app.mode === 'crop') applyCrop();
    beginEdit(axis === 'h' ? 'Flip horizontal' : 'Flip vertical');
    changeRecipe((r) => {
        const g = r.geometry || (r.geometry = {});
        const key = axis === 'h' ? 'flip_h' : 'flip_v';
        if (g[key]) delete g[key];
        else g[key] = true;
        if (g.angle) g.angle = -g.angle;
        if (g.crop) {
            g.crop = axis === 'h'
                ? { ...g.crop, x: 1 - g.crop.x - g.crop.w }
                : { ...g.crop, y: 1 - g.crop.y - g.crop.h };
        }
        (r.masks || []).forEach((m) => {
            flipMask(m, axis);
            roundMask(m);
        });
    }, { final: true });
}

function roundRect(r) {
    const f = (v) => Math.round(v * 1e5) / 1e5;
    return { x: f(r.x), y: f(r.y), w: f(r.w), h: f(r.h) };
}

function enterCrop() {
    if (!sourceAvailable()) return;
    if (zoom.active) exitZoom(false);
    setMode('crop');
    crop.start(getPath(app.recipe, 'geometry.crop'), panels.aspect);
    panels.setCropMode(true);
    refreshPreview();
}

function applyCrop() {
    if (app.mode !== 'crop') return;
    const rect = crop.value();
    setMode('normal');
    const full = rect.x <= 0.001 && rect.y <= 0.001 && rect.w >= 0.999 && rect.h >= 0.999;
    const next = full ? undefined : roundRect(rect);
    if (canonical(getPath(app.recipe, 'geometry.crop') || null) === canonical(next || null)) {
        refreshPreview();
        return;
    }
    beginEdit('Crop');
    changeRecipe((r) => setPath(r, 'geometry.crop', next), { final: true, label: full ? 'Remove crop' : 'Crop' });
}

function cancelCrop() {
    if (app.mode !== 'crop') return;
    setMode('normal');
    refreshPreview();
}

function setMode(mode) {
    if (app.mode === mode) return;
    if (app.mode === 'crop') {
        crop.stop();
        panels.setCropMode(false);
    }
    app.mode = mode;
    els.stage.classList.toggle('picking', mode === 'picker');
    els.beforeBadge.hidden = mode !== 'before';
    els.before.classList.toggle('active', mode === 'before');
    panels.setPickerActive(mode === 'picker');
    syncOverlay();
}

function toggleBefore() {
    if (app.mode === 'crop') return;
    setMode(app.mode === 'before' ? 'normal' : 'before');
    refreshPreview();
}

function enterZoom(x, y) {
    if (!sourceAvailable() || !app.fullW || app.mode === 'crop') return;
    zoom.setFull(app.fullW, app.fullH);
    zoom.enter(x, y);
    fitPreviewer.cancel();
    els.zoomButton.classList.add('active');
    syncOverlay();
    zoomPreviewer.request(previewRecipe(), { final: true });
}

function exitZoom(refresh = true) {
    if (!zoom.active) return;
    zoomPreviewer.cancel();
    zoom.exit();
    els.zoomButton.classList.remove('active');
    els.quality.hidden = true;
    syncOverlay();
    if (refresh) refreshPreview();
}

function toggleZoom() {
    if (zoom.active) exitZoom();
    else enterZoom(0.5, 0.5);
}

function toggleClip() {
    app.clip = !app.clip;
    els.clipmask.hidden = !app.clip;
    els.clipButton.classList.toggle('active', app.clip);
    if (app.clip) histogram.drawClipMask(els.image, els.clipmask);
}

function togglePanels() {
    document.body.classList.toggle('dv-hide-panels');
    requestAnimationFrame(() => {
        layoutOverlays();
        zoom.layout();
        refreshPreview();
    });
}

function imageBox() {
    const s = els.stage.getBoundingClientRect();
    const i = els.image.getBoundingClientRect();
    return { left: i.left - s.left, top: i.top - s.top, width: i.width, height: i.height };
}

// fitImage sizes the fit preview explicitly to "contain" the stage, so a
// reduced-resolution drag frame occupies the same box as a final frame
// instead of shrinking to its natural size.
function fitImage() {
    const img = els.image;
    if (!img.naturalWidth || !img.naturalHeight) return;
    const cs = getComputedStyle(els.stage);
    const aw = els.stage.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    const ah = els.stage.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
    if (aw <= 0 || ah <= 0) return;
    const ar = img.naturalWidth / img.naturalHeight;
    let w = aw;
    let h = aw / ar;
    if (h > ah) {
        h = ah;
        w = ah * ar;
    }
    img.style.width = `${Math.floor(w)}px`;
    img.style.height = `${Math.floor(h)}px`;
}

function layoutOverlays() {
    fitImage();
    const b = imageBox();
    for (const el of [els.clipmask, els.crop]) {
        Object.assign(el.style, { left: `${b.left}px`, top: `${b.top}px`, width: `${b.width}px`, height: `${b.height}px` });
    }
    crop.render();
    masksOverlay.layout(b);
}

function onFitFrame(frame) {
    if (frame.fullW && frame.fullH) {
        app.fullW = frame.fullW;
        app.fullH = frame.fullH;
    }
    els.quality.hidden = frame.quality !== 'fast';
    histogram.update(els.image);
    panels.setHistogram(histogram.lum);
    layoutOverlays();
    if (app.mode === 'crop') crop.frameChanged();
    if (app.clip) histogram.drawClipMask(els.image, els.clipmask);
    perf.record(frame);
    // Once per page, pre-decode full resolution so the first 1:1 is quick.
    if (!app.warmed && frame.final) {
        app.warmed = true;
        setTimeout(() => api.warm(app.id, app.recipe), 1500);
    }
}

function onZoomFrame(frame) {
    zoom.setFull(frame.fullW, frame.fullH);
    zoom.show(frame.region);
    els.quality.hidden = frame.quality !== 'fast';
    perf.record(frame);
}

async function loadHistory() {
    try {
        const { items } = await api.history(app.id);
        els.history.innerHTML = '';
        if (!items.length) {
            const li = document.createElement('li');
            li.className = 'dv-empty';
            li.textContent = cfg.staged ? 'History starts after adding to the gallery' : 'No saved edits yet';
            els.history.appendChild(li);
            return;
        }
        items.forEach((item) => {
            const li = document.createElement('li');
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.innerHTML = '<span class="dv-h-rev"></span><span class="dv-h-label"></span><time></time>';
            btn.querySelector('.dv-h-rev').textContent = `#${item.revision}`;
            btn.querySelector('.dv-h-label').textContent = item.label || 'Edit';
            btn.querySelector('time').textContent = new Date(item.created_at).toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' });
            btn.addEventListener('click', () => {
                if (!sourceAvailable()) return;
                beginEdit(`Restore #${item.revision}`);
                changeRecipe((r) => replaceRecipe(r, item.recipe), { final: true, label: `Restore #${item.revision}` });
            });
            li.appendChild(btn);
            els.history.appendChild(li);
        });
    } catch (e) {
        els.history.innerHTML = '';
    }
}

function buildStrip() {
    const items = cfg.strip || [];
    const idx = items.findIndex((i) => i.id === app.id);
    app.stripIndex = idx;
    $('dv-name').textContent = cfg.staged ? `${cfg.filename} · Unsorted` : cfg.filename;
    $('dv-counter').textContent = idx >= 0 ? `${idx + 1} / ${items.length}` : '';
    $('dv-prev').disabled = idx <= 0;
    $('dv-next').disabled = idx < 0 || idx >= items.length - 1;
    items.forEach((item, i) => {
        const a = document.createElement('a');
        a.href = item.href;
        a.className = 'dv-strip-item'
            + (i === idx ? ' active' : '')
            + (item.edited ? ' edited' : '')
            + (item.hidden ? ' hidden-photo' : '');
        a.title = item.name;
        a.dataset.id = String(item.id);
        const img = document.createElement('img');
        img.loading = 'lazy';
        img.alt = '';
        img.src = item.thumb;
        a.appendChild(img);
        a.addEventListener('click', (e) => {
            e.preventDefault();
            goTo(item.href);
        });
        els.strip.appendChild(a);
    });
    const active = els.strip.querySelector('.active');
    if (active) active.scrollIntoView({ inline: 'center', block: 'nearest' });
}

function markStripEdited(on) {
    const a = els.strip.querySelector(`[data-id="${app.id}"]`);
    if (a) a.classList.toggle('edited', on);
}

function step(delta) {
    const next = (cfg.strip || [])[app.stripIndex + delta];
    if (next) goTo(next.href);
}

async function goTo(href) {
    await flush();
    window.location.href = href;
}

async function goBack() {
    await flush();
    window.location.href = cfg.back;
}

function onEscape() {
    if (zoom.active && app.mode !== 'picker') {
        exitZoom();
        return;
    }
    if (masksOverlay.visible && app.maskId) {
        selectMask(null);
        return;
    }
    if (app.mode === 'crop') cancelCrop();
    else if (app.mode !== 'normal') {
        setMode('normal');
        refreshPreview();
    } else goBack();
}

function wire() {
    $('dv-back').addEventListener('click', goBack);
    $('dv-prev').addEventListener('click', () => step(-1));
    $('dv-next').addEventListener('click', () => step(1));
    els.undo.addEventListener('click', undo);
    els.redo.addEventListener('click', redo);
    els.before.addEventListener('click', toggleBefore);
    els.zoomButton.addEventListener('click', toggleZoom);
    els.clipButton.addEventListener('click', toggleClip);
    $('dv-copy').addEventListener('click', copySettings);
    $('dv-paste').addEventListener('click', () => pasteSettings());
    $('dv-reset').addEventListener('click', resetAll);
    $('dv-panels').addEventListener('click', togglePanels);
    $('dv-preset-save').addEventListener('click', savePreset);
    exporter = setupExport($('dv-export'), $('dv-export-dialog'), {
        id: app.id,
        getRecipe: () => app.recipe,
        toast,
        showError,
    });

    els.image.addEventListener('click', (e) => {
        if (zoom.active || masksOverlay.visible) return;
        const r = els.image.getBoundingClientRect();
        const x = (e.clientX - r.left) / r.width;
        const y = (e.clientY - r.top) / r.height;
        if (app.mode === 'picker') pickAt(x, y);
        else if (app.mode === 'normal' || app.mode === 'before') enterZoom(x, y);
    });

    bindShortcuts({
        '\\': toggleBefore,
        z: toggleZoom,
        c: () => (app.mode === 'crop' ? applyCrop() : enterCrop()),
        w: () => setMode(app.mode === 'picker' ? 'normal' : 'picker'),
        j: toggleClip,
        h: togglePanels,
        o: () => { if (masksOverlay.visible) masksOverlay.toggleWeights(); },
        r: () => rotate(90),
        R: () => rotate(-90),
        Enter: () => { if (app.mode === 'crop') applyCrop(); },
        Escape: onEscape,
        ArrowLeft: () => step(-1),
        ArrowRight: () => step(1),
        'Mod+z': undo,
        'Mod+Z': redo,
        'Mod+y': redo,
        'Mod+c': copySettings,
        'Mod+v': () => pasteSettings(),
        'Mod+V': pasteSelective,
        'Mod+e': () => { if (!cfg.staged && sourceAvailable()) exporter.open(); },
    });

    let resizeTimer = 0;
    window.addEventListener('resize', () => {
        layoutOverlays();
        zoom.layout();
        histogram.draw();
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(() => refreshPreview(), 200);
    });

    window.addEventListener('beforeunload', () => {
        if (isDirty() && sourceAvailable()) api.saveOnUnload(app.id, app.recipe, app.revision, app.label);
    });
}

function setSheet(open) {
    document.body.classList.toggle('dv-sheet-open', open);
    requestAnimationFrame(() => {
        layoutOverlays();
        zoom.layout();
        histogram.draw();
    });
    clearTimeout(app.sheetTimer);
    app.sheetTimer = setTimeout(() => refreshPreview(), 250);
}

// wireMobile: bottom-sheet panel toggle, swipe between photos, perf HUD key.
function wireMobile() {
    $('dv-sheet-handle').addEventListener('click', () => setSheet(!document.body.classList.contains('dv-sheet-open')));
    mobileQuery.addEventListener('change', (e) => {
        if (!e.matches) document.body.classList.remove('dv-sheet-open');
        layoutOverlays();
        refreshPreview();
    });

    let swipe = null;
    els.stage.addEventListener('pointerdown', (e) => {
        if (e.pointerType !== 'touch' || zoom.active || app.mode !== 'normal' || masksOverlay.visible) {
            swipe = null;
            return;
        }
        swipe = { x: e.clientX, y: e.clientY, t: performance.now() };
    });
    els.stage.addEventListener('pointerup', (e) => {
        const s = swipe;
        swipe = null;
        if (!s) return;
        const dx = e.clientX - s.x;
        const dy = e.clientY - s.y;
        if (performance.now() - s.t < 600 && Math.abs(dx) > 80 && Math.abs(dy) < 60) step(dx < 0 ? 1 : -1);
    });
    els.stage.addEventListener('pointercancel', () => { swipe = null; });

    bindShortcuts({ p: () => perf.toggle() });
}

function wireAuto() {
    els.autoButton.addEventListener('click', () => runAuto());
    bindShortcuts({ A: () => runAuto() });
}

async function init() {
    buildStrip();
    wire();
    wireMobile();
    wireAuto();
    updateUndoButtons();
    presets.load();
    if (cfg.staged) $('dv-export').hidden = true;
    try {
        const st = await api.state(app.id);
        applyState(st, true);
        if (st.source.available) refreshPreview();
        else if (!cfg.staged) {
            els.image.addEventListener('load', layoutOverlays, { once: true });
            els.image.src = `/thumb/large/${app.id}`;
        }
        loadHistory();
        schedulePoll();
    } catch (err) {
        showError(`Cannot load photo: ${err.message}`);
    }
}

init();
