import { TABS, MODULES, DEMOSAIC, HIGHLIGHTS, ASPECTS, HSL_BANDS, tempToPos, posToTemp } from './schema.js';
import { ColorWheel } from './wheel.js';
import { createSlider } from './slider.js';
import { CurveEditor } from './curve.js';
import { clone, getPath, setPath } from './recipe.js';

const TAB_KEY = 'photodock-develop-tab';
const ASPECT_KEY = 'photodock-develop-aspect';

function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
}

function button(label, title, onClick) {
    const b = el('button', 'dv-btn', label);
    b.type = 'button';
    if (title) b.title = title;
    b.addEventListener('click', onClick);
    return b;
}

function select(options, onChange) {
    const s = el('select', 'dv-select');
    options.forEach(([v, l]) => {
        const o = el('option', null, l);
        o.value = v;
        s.appendChild(o);
    });
    s.addEventListener('change', () => onChange(s.value));
    return s;
}

function field(label, control) {
    const f = el('label', 'dv-field');
    f.append(el('span', null, label), control);
    return f;
}

function checkbox(label, onChange) {
    const wrap = el('label', 'dv-check');
    const input = el('input');
    input.type = 'checkbox';
    input.addEventListener('change', () => onChange(input.checked));
    wrap.append(input, document.createTextNode(label));
    return { el: wrap, input };
}

const signedFmt = (v) => (v > 0 ? '+' : '') + Math.round(v);

function storage(key, fallback) {
    try {
        return localStorage.getItem(key) || fallback;
    } catch (e) {
        return fallback;
    }
}

export class Panels {
    constructor(tabsEl, modulesEl, ctrl) {
        this.ctrl = ctrl;
        this.ctx = { raw: false, asShot: null, defaults: {} };
        this.syncers = [];
        this.disablers = [];
        this.sections = [];
        this.tabButtons = TABS.map((t) => {
            const b = button(t.label, null, () => this.showTab(t.id));
            b.dataset.tab = t.id;
            if (t.rawOnly) b.dataset.rawOnly = '1';
            tabsEl.appendChild(b);
            return b;
        });
        MODULES.forEach((m) => modulesEl.appendChild(this.buildModule(m)));
        const saved = storage(TAB_KEY, TABS[0].id);
        this.showTab(TABS.some((t) => t.id === saved) ? saved : TABS[0].id);
    }

    showTab(id) {
        if (id === 'raw' && !this.ctx.raw) id = 'basic';
        this.tab = id;
        try { localStorage.setItem(TAB_KEY, id); } catch (e) {}
        this.tabButtons.forEach((b) => {
            b.classList.toggle('active', b.dataset.tab === id);
            b.hidden = b.dataset.rawOnly === '1' && !this.ctx.raw;
        });
        this.sections.forEach((s) => {
            s.hidden = s.dataset.tab !== id || (s.dataset.rawOnly === '1' && !this.ctx.raw);
        });
        if (this.ctrl.tabChanged) this.ctrl.tabChanged(id);
    }

    setContext(ctx) {
        this.ctx = { ...this.ctx, ...ctx };
        this.showTab(this.tab);
    }

    refresh(recipe) {
        this.syncers.forEach((fn) => fn(recipe));
    }

    setDisabled(d) {
        this.disablers.forEach((fn) => fn(d));
    }

    setHistogram(bins) {
        if (this.curve && bins) this.curve.setHistogram(bins);
    }

    setPickerActive(on) {
        if (this.pickButton) this.pickButton.classList.toggle('active', on);
    }

    setCropMode(on) {
        this.cropButton.textContent = on ? 'Apply crop' : 'Crop';
        this.cropButton.classList.toggle('primary', on);
        this.cropCancel.hidden = !on;
    }

    get aspect() {
        return this.aspectSelect.value;
    }

    track(control) {
        this.disablers.push((d) => (control.setDisabled ? control.setDisabled(d) : (control.disabled = d)));
    }

    buildModule(mod) {
        const sec = el('section', 'dv-module');
        sec.dataset.tab = mod.tab;
        if (mod.rawOnly) sec.dataset.rawOnly = '1';
        const head = el('header', 'dv-module-head');
        const title = el('h4', null, mod.title);
        title.addEventListener('click', () => sec.classList.toggle('collapsed'));
        const actions = el('div', 'dv-row');
        if (mod.auto) {
            const auto = button('Auto', `Auto ${mod.title.toLowerCase()}`, () => this.ctrl.auto(mod.auto));
            auto.classList.add('dv-icon');
            this.track(auto);
            actions.appendChild(auto);
        }
        const reset = button('↺', `Reset ${mod.title.toLowerCase()}`, () => {
            this.ctrl.begin(`Reset ${mod.title}`);
            this.ctrl.change((r) => mod.paths.forEach((p) => setPath(r, p, undefined)), { final: true, label: `Reset ${mod.title}` });
        });
        reset.classList.add('dv-icon');
        this.track(reset);
        actions.appendChild(reset);
        head.append(title, actions);
        const body = el('div', 'dv-module-body');
        sec.append(head, body);

        if (mod.kind === 'wb') this.buildWB(body);
        else if (mod.kind === 'curve') this.buildCurve(body);
        else if (mod.kind === 'geometry') this.buildGeometry(body);
        else if (mod.kind === 'lens') this.buildLens(body);
        else if (mod.kind === 'raw') this.buildRaw(body);
        else if (mod.kind === 'hsl') this.buildHSL(body);
        else if (mod.kind === 'grading') this.buildGrading(body);
        else if (mod.kind === 'bw') this.buildBW(body);
        else if (mod.kind === 'masks') this.buildMasks(body);
        else this.buildSliders(body, mod.sliders);
        if (mod.hint) body.appendChild(el('p', 'dv-hint', mod.hint));

        this.sections.push(sec);
        return sec;
    }

    slider(spec, write, opts = {}) {
        const s = createSlider(spec, {
            start: () => this.ctrl.begin(spec.label),
            input: (v) => this.ctrl.change((r) => write(r, v), { quality: opts.quality ? opts.quality() : 'accurate' }),
            commit: (v) => this.ctrl.change((r) => write(r, v), { final: true, label: `${spec.label} ${spec.format(v)}` }),
            reset: () => this.ctrl.change((r) => (opts.reset ? opts.reset(r) : write(r, undefined)), { final: true, label: `${spec.label} reset` }),
        });
        this.track(s);
        return s;
    }

    buildSliders(body, sliders) {
        sliders.forEach((spec) => {
            const s = this.slider(spec, (r, v) => setPath(r, spec.path, v));
            body.appendChild(s.el);
            this.syncers.push((r) => s.set(getPath(r, spec.path) ?? spec.fallback ?? 0));
        });
    }

    buildWB(body) {
        const asShot = () => this.ctx.asShot || { temp: 5500, tint: 0 };
        const ensureCustom = (r) => {
            if (!r.wb || r.wb.mode !== 'custom') {
                const a = asShot();
                r.wb = { mode: 'custom', temp: a.temp, tint: a.tint };
            }
        };
        const mode = select([['as_shot', 'As shot'], ['custom', 'Custom']], (v) => {
            this.ctrl.begin('White balance mode');
            this.ctrl.change((r) => {
                if (v === 'custom') ensureCustom(r);
                else r.wb = { mode: 'as_shot' };
            }, { final: true, label: v === 'custom' ? 'Custom white balance' : 'As shot white balance' });
        });
        const pick = button('Pick', 'Click a neutral area (W)', () => this.ctrl.wbPick());
        const auto = button('Auto', 'Auto white balance', () => this.ctrl.wbAuto());
        this.pickButton = pick;
        [mode, pick, auto].forEach((c) => this.track(c));
        const row = el('div', 'dv-row');
        row.append(mode, pick, auto);

        const quality = () => (this.ctx.raw ? 'fast' : 'accurate');
        const toAsShot = (r) => { r.wb = { mode: 'as_shot' }; };
        const temp = this.slider({
            label: 'Temperature', min: 2000, max: 25000, step: 10,
            posMin: 0, posMax: 1000, posStep: 1, toPos: tempToPos, fromPos: posToTemp,
            format: (v) => `${Math.round(v)} K`, cls: 'temp',
        }, (r, v) => { ensureCustom(r); r.wb.temp = v; }, { quality, reset: toAsShot });
        const tint = this.slider({
            label: 'Tint', min: -150, max: 150, step: 1,
            format: (v) => (v > 0 ? '+' : '') + Math.round(v), cls: 'tint',
        }, (r, v) => { ensureCustom(r); r.wb.tint = v; }, { quality, reset: toAsShot });

        body.append(row, temp.el, tint.el);
        this.syncers.push((r) => {
            const custom = !!(r.wb && r.wb.mode === 'custom');
            const a = asShot();
            mode.value = custom ? 'custom' : 'as_shot';
            temp.set(custom ? r.wb.temp : a.temp);
            tint.set(custom ? (r.wb.tint || 0) : a.tint);
        });
    }

    buildCurve(body) {
        const write = (pts) => (r) => setPath(r, 'tone.curve', pts ? clone(pts) : undefined);
        const editor = new CurveEditor({
            onStart: () => this.ctrl.begin('Tone curve'),
            onInput: (pts) => this.ctrl.change(write(pts), {}),
            onCommit: (pts) => this.ctrl.change(write(pts), { final: true, label: 'Tone curve' }),
        });
        this.curve = editor;
        this.track(editor);
        body.append(editor.el, el('p', 'dv-hint', 'Click to add a point · drag off the graph or double-click to remove'));
        this.syncers.push((r) => {
            if (!editor.dragging) editor.set(getPath(r, 'tone.curve'));
        });
    }

    buildGeometry(body) {
        const rotateRow = el('div', 'dv-row');
        const buttons = [
            button('⟲ 90°', 'Rotate left (Shift+R)', () => this.ctrl.rotate(-90)),
            button('⟳ 90°', 'Rotate right (R)', () => this.ctrl.rotate(90)),
            button('⇋', 'Flip horizontal', () => this.ctrl.flip('h')),
            button('⇵', 'Flip vertical', () => this.ctrl.flip('v')),
        ];
        buttons.forEach((b) => this.track(b));
        rotateRow.append(...buttons);

        const angle = this.slider({
            path: 'geometry.angle', label: 'Straighten', min: -45, max: 45, step: 0.05,
            format: (v) => (v > 0 ? '+' : '') + v.toFixed(2) + '°',
        }, (r, v) => setPath(r, 'geometry.angle', v));
        this.syncers.push((r) => angle.set(getPath(r, 'geometry.angle') || 0));

        const cropRow = el('div', 'dv-row');
        this.cropButton = button('Crop', 'Crop (C)', () => this.ctrl.cropToggle());
        this.cropCancel = button('Cancel', 'Cancel crop (Esc)', () => this.ctrl.cropCancel());
        this.cropCancel.hidden = true;
        this.aspectSelect = select(ASPECTS, (v) => {
            try { localStorage.setItem(ASPECT_KEY, v); } catch (e) {}
            this.ctrl.aspect(v);
        });
        this.aspectSelect.value = storage(ASPECT_KEY, 'free');
        const swap = button('⤢', 'Swap crop orientation', () => this.ctrl.swapAspect());
        [this.cropButton, this.aspectSelect, swap].forEach((c) => this.track(c));
        cropRow.append(this.cropButton, this.cropCancel, this.aspectSelect, swap);

        body.append(rotateRow, angle.el, cropRow, el('p', 'dv-hint', 'Enter applies the crop, Esc cancels.'));
    }

    buildRaw(body) {
        const choose = (path, label) => (v) => {
            this.ctrl.begin(label);
            this.ctrl.change((r) => setPath(r, path, v || undefined), { final: true, label: `${label}: ${v || 'default'}` });
        };
        const demosaic = select(DEMOSAIC, choose('raw.demosaic', 'Demosaic'));
        const highlights = select(HIGHLIGHTS, choose('raw.highlights', 'Highlight recovery'));
        [demosaic, highlights].forEach((c) => this.track(c));
        body.append(
            field('Demosaic', demosaic),
            field('Highlight recovery', highlights),
            el('p', 'dv-hint', 'Demosaic differences are visible only at full resolution, i.e. in the published image. Changing these re-decodes the RAW (a few seconds).'),
        );
        this.syncers.push((r) => {
            demosaic.value = getPath(r, 'raw.demosaic') || '';
            highlights.value = getPath(r, 'raw.highlights') || '';
            const d = this.ctx.defaults && this.ctx.defaults.raw;
            if (d) {
                demosaic.options[0].textContent = `Default (${(d.demosaic || '').toUpperCase()})`;
                highlights.options[0].textContent = `Default (${d.highlights || ''})`;
            }
        });
    }

    buildLens(body) {
        const lensName = (id) => (id || '').replace('|', ' ');
        const ensure = (r) => {
            r.lens = { ...(r.lens || {}), enabled: true };
            return r.lens;
        };
        this.extraProfiles = new Map();

        const enable = checkbox(' Enable lens correction', (on) => {
            this.ctrl.begin('Lens correction');
            this.ctrl.change((r) => {
                if (on) ensure(r);
                else delete r.lens;
            }, { final: true, label: on ? 'Enable lens correction' : 'Disable lens correction' });
        });
        const info = el('p', 'dv-hint');
        const profile = el('select', 'dv-select dv-grow');
        profile.addEventListener('change', () => {
            this.ctrl.begin('Lens profile');
            this.ctrl.change((r) => {
                const l = ensure(r);
                if (profile.value) l.profile = profile.value;
                else delete l.profile;
            }, { final: true, label: 'Lens profile' });
        });

        const search = el('input', 'dv-input');
        search.type = 'search';
        search.placeholder = 'Search the Lensfun database…';
        const results = el('div', 'dv-lens-results');
        let timer = 0;
        search.addEventListener('input', () => {
            clearTimeout(timer);
            const q = search.value.trim();
            if (q.length < 2) {
                results.innerHTML = '';
                return;
            }
            timer = setTimeout(async () => {
                const items = await this.ctrl.lensSearch(q);
                results.innerHTML = '';
                items.forEach((it) => {
                    const b = button(`${it.maker} ${it.model}`, it.id, () => {
                        this.extraProfiles.set(it.id, it);
                        results.innerHTML = '';
                        search.value = '';
                        this.ctrl.begin('Lens profile');
                        this.ctrl.change((r) => { ensure(r).profile = it.id; }, { final: true, label: 'Lens profile' });
                    });
                    b.classList.add('dv-lens-hit');
                    results.appendChild(b);
                });
                if (!items.length) results.appendChild(el('span', 'dv-hint', 'No matches'));
            }, 250);
        });

        const applied = el('p', 'dv-hint');
        const remember = button('Remember for this lens', 'Use this profile automatically for every photo taken with this lens', () => {
            const id = profile.value || (this.ctx.lens && this.ctx.lens.applied);
            if (id) this.ctrl.lensConfirm(id);
        });
        const forget = button('Forget', 'Remove the remembered profile for this lens', () => this.ctrl.lensConfirm(''));
        const actions = el('div', 'dv-row');
        actions.append(remember, forget);

        const pct = (v) => `${Math.round(100 + v)}%`;
        const dist = this.slider({ label: 'Distortion', min: -100, max: 100, step: 1, format: pct }, (r, v) => { ensure(r); setPath(r, 'lens.distortion', v); });
        const vig = this.slider({ label: 'Vignetting', min: -100, max: 100, step: 1, format: pct }, (r, v) => { ensure(r); setPath(r, 'lens.vignetting', v); });
        const tca = checkbox(' Chromatic aberration (TCA)', (on) => {
            this.ctrl.begin('Lens TCA');
            this.ctrl.change((r) => {
                const l = ensure(r);
                if (on) delete l.no_tca;
                else l.no_tca = true;
            }, { final: true, label: on ? 'TCA on' : 'TCA off' });
        });

        [enable.input, profile, search, remember, forget, tca.input].forEach((c) => this.track(c));
        body.append(enable.el, info, profile, search, results, applied, actions, dist.el, vig.el, tca.el,
            el('p', 'dv-hint', 'Distortion and vignetting strength: 100% is the Lensfun profile as calibrated.'));

        this.syncers.push((r) => {
            const L = this.ctx.lens || {};
            const on = !!(r.lens && r.lens.enabled);
            enable.input.checked = on;

            const opts = [['', L.confirmed ? `Auto — ${lensName(L.confirmed)}` : 'Auto — no remembered profile']];
            const seen = new Set();
            (L.candidates || []).forEach((c) => {
                seen.add(c.id);
                opts.push([c.id, `${c.maker} ${c.model}`]);
            });
            this.extraProfiles.forEach((c, id) => {
                if (!seen.has(id)) {
                    seen.add(id);
                    opts.push([id, `${c.maker} ${c.model}`]);
                }
            });
            const cur = r.lens && r.lens.profile;
            if (cur && !seen.has(cur)) opts.push([cur, lensName(cur)]);
            profile.innerHTML = '';
            opts.forEach(([v, l]) => {
                const o = el('option', null, l);
                o.value = v;
                profile.appendChild(o);
            });
            profile.value = cur || '';

            const parts = [L.exif_lens || 'Lens not reported in EXIF'];
            if (L.focal) parts.push(`${+L.focal.toFixed(1)} mm`);
            if (L.aperture) parts.push(`f/${+L.aperture.toFixed(1)}`);
            if (L.camera_crop) parts.push(`crop ×${+L.camera_crop.toFixed(2)}`);
            info.textContent = parts.join(' · ');

            if (!L.available) applied.textContent = 'Lensfun database not found on the server.';
            else if (!on) applied.textContent = '';
            else applied.textContent = L.applied ? `Applied: ${lensName(L.applied)}` : 'No profile selected — correction is inactive.';

            dist.set(getPath(r, 'lens.distortion') || 0);
            vig.set(getPath(r, 'lens.vignetting') || 0);
            tca.input.checked = !(r.lens && r.lens.no_tca);
            remember.disabled = !L.exif_lens || !(profile.value || L.applied);
            forget.disabled = !L.confirmed;
        });
    }

    buildHSL(body) {
        const modes = [['h', 'Hue'], ['s', 'Saturation'], ['l', 'Luminance']];
        const tabs = el('div', 'dv-row dv-subtabs');
        const groups = {};
        const show = (m) => {
            tabs.querySelectorAll('button').forEach((b) => b.classList.toggle('active', b.dataset.m === m));
            Object.entries(groups).forEach(([k, g]) => { g.hidden = k !== m; });
        };
        modes.forEach(([m, label]) => {
            const b = button(label, null, () => show(m));
            b.dataset.m = m;
            tabs.appendChild(b);
            const g = el('div', 'dv-module-body');
            g.style.padding = '0';
            groups[m] = g;
            HSL_BANDS.forEach(([band, name, color]) => {
                const path = `color.hsl.${band}.${m}`;
                const s = this.slider({ label: name, min: -100, max: 100, step: 1, format: signedFmt, cls: 'band' }, (r, v) => setPath(r, path, v));
                s.el.style.setProperty('--band', color);
                g.appendChild(s.el);
                this.syncers.push((r) => s.set(getPath(r, path) || 0));
            });
        });
        body.append(tabs, ...Object.values(groups));
        show('h');
    }

    buildGrading(body) {
        const grid = el('div', 'dv-wheels');
        [['shadows', 'Shadows'], ['midtones', 'Midtones'], ['highlights', 'Highlights'], ['global', 'Global']].forEach(([key, label]) => {
            const base = `color.grading.${key}`;
            const write = (h, s) => (r) => {
                setPath(r, `${base}.sat`, s);
                setPath(r, `${base}.hue`, s ? h : undefined);
            };
            const wheel = new ColorWheel(label, {
                onStart: () => this.ctrl.begin(`${label} grading`),
                onInput: (h, s) => this.ctrl.change(write(h, s), {}),
                onCommit: (h, s) => this.ctrl.change(write(h, s), { final: true, label: `${label} grading` }),
                onReset: () => {
                    this.ctrl.begin(`${label} grading`);
                    this.ctrl.change(write(0, 0), { final: true, label: `${label} grading reset` });
                },
            });
            const lum = this.slider({ label: 'Luminance', min: -100, max: 100, step: 1, format: signedFmt }, (r, v) => setPath(r, `${base}.lum`, v));
            const box = el('div', 'dv-wheel-box');
            box.append(wheel.el, lum.el);
            grid.appendChild(box);
            this.track(wheel);
            this.syncers.push((r) => {
                if (!wheel.drag) wheel.set(getPath(r, `${base}.hue`) || 0, getPath(r, `${base}.sat`) || 0);
                lum.set(getPath(r, `${base}.lum`) || 0);
            });
        });
        // Blending is stored as an offset from 50 so zero stays neutral.
        const blending = this.slider({ label: 'Blending', min: 0, max: 100, step: 1, format: (v) => String(Math.round(v)) },
            (r, v) => setPath(r, 'color.grading.blending', v === undefined ? undefined : v - 50));
        const balance = this.slider({ label: 'Balance', min: -100, max: 100, step: 1, format: signedFmt },
            (r, v) => setPath(r, 'color.grading.balance', v));
        this.syncers.push((r) => {
            blending.set((getPath(r, 'color.grading.blending') || 0) + 50);
            balance.set(getPath(r, 'color.grading.balance') || 0);
        });
        body.append(grid, blending.el, balance.el);
    }

    buildBW(body) {
        const enable = checkbox(' Convert to black & white', (on) => {
            this.ctrl.begin('Black & white');
            this.ctrl.change((r) => setPath(r, on ? 'color.bw.enabled' : 'color.bw', on ? true : undefined),
                { final: true, label: on ? 'Black & white' : 'Color' });
        });
        this.track(enable.input);
        body.appendChild(enable.el);
        HSL_BANDS.forEach(([band, name, color]) => {
            const path = `color.bw.mix.${band}`;
            const s = this.slider({ label: name, min: -100, max: 100, step: 1, format: signedFmt, cls: 'band' }, (r, v) => {
                setPath(r, 'color.bw.enabled', true);
                setPath(r, path, v);
            });
            s.el.style.setProperty('--band', color);
            body.appendChild(s.el);
            this.syncers.push((r) => s.set(getPath(r, path) || 0));
        });
        this.syncers.push((r) => { enable.input.checked = !!getPath(r, 'color.bw.enabled'); });
    }

    setMaskSelection(id) {
        this.maskSel = id;
    }

    buildMasks(body) {
        this.maskSel = null;
        const idx = (r) => (r.masks || []).findIndex((m) => m.id === this.maskSel);
        const ev = (v) => (v > 0 ? '+' : '') + v.toFixed(2) + ' EV';

        const addRow = el('div', 'dv-row');
        const addLinear = button('+ Gradient', 'Add a linear gradient mask', () => this.ctrl.maskAdd('linear'));
        const addRadial = button('+ Radial', 'Add a radial mask', () => this.ctrl.maskAdd('radial'));
        const overlay = button('Overlay', 'Show where masks apply (O)', () => this.ctrl.maskWeights());
        addRow.append(addLinear, addRadial, overlay);

        const list = el('div', 'dv-mask-list');
        const editor = el('div', 'dv-module-body');
        editor.style.padding = '0';

        const enabled = checkbox(' Enabled', (on) => {
            this.ctrl.begin('Mask');
            this.ctrl.change((r) => {
                const i = idx(r);
                if (i < 0) return;
                if (on) delete r.masks[i].off;
                else r.masks[i].off = true;
            }, { final: true, label: on ? 'Enable mask' : 'Disable mask' });
        });
        const invert = checkbox(' Invert', (on) => {
            this.ctrl.begin('Mask');
            this.ctrl.change((r) => {
                const i = idx(r);
                if (i < 0) return;
                if (on) r.masks[i].invert = true;
                else delete r.masks[i].invert;
            }, { final: true, label: 'Invert mask' });
        });
        const dup = button('Duplicate', 'Duplicate this mask', () => this.ctrl.maskDuplicate(this.maskSel));
        const del = button('Delete', 'Delete this mask', () => this.ctrl.maskDelete(this.maskSel));
        del.classList.add('danger');
        const row = el('div', 'dv-row');
        row.append(enabled.el, invert.el, dup, del);

        const feather = this.slider({ label: 'Feather', min: 0, max: 100, step: 1, format: (v) => String(Math.round(v)) }, (r, v) => {
            const i = idx(r);
            if (i >= 0) setPath(r, `masks.${i}.feather`, v);
        });
        const specs = [
            ['exposure', 'Exposure', -4, 4, 0.01, ev],
            ['contrast', 'Contrast', -100, 100, 1, signedFmt],
            ['highlights', 'Highlights', -100, 100, 1, signedFmt],
            ['shadows', 'Shadows', -100, 100, 1, signedFmt],
            ['temp', 'Temperature', -100, 100, 1, signedFmt],
            ['tint', 'Tint', -100, 100, 1, signedFmt],
            ['saturation', 'Saturation', -100, 100, 1, signedFmt],
            ['clarity', 'Clarity', -100, 100, 1, signedFmt],
        ];
        const adjust = specs.map(([key, label, min, max, step, format]) => ({
            key,
            slider: this.slider({ label, min, max, step, format }, (r, v) => {
                const i = idx(r);
                if (i >= 0) setPath(r, `masks.${i}.adjust.${key}`, v);
            }),
        }));
        editor.append(row, feather.el, ...adjust.map((a) => a.slider.el));

        [addLinear, addRadial, overlay, enabled.input, invert.input, dup, del].forEach((c) => this.track(c));
        body.append(addRow, list, editor, el('p', 'dv-hint', 'Drag the handles on the photo. O toggles the overlay, Esc deselects.'));

        this.syncers.push((r) => {
            const masks = r.masks || [];
            if (this.maskSel && !masks.some((m) => m.id === this.maskSel)) this.maskSel = null;
            list.innerHTML = '';
            masks.forEach((m, i) => {
                const b = button(`${m.type === 'linear' ? 'Gradient' : 'Radial'} ${i + 1}${m.off ? ' · off' : ''}`, null, () => this.ctrl.maskSelect(m.id));
                b.classList.toggle('active', m.id === this.maskSel);
                list.appendChild(b);
            });
            if (!masks.length) list.appendChild(el('span', 'dv-hint', 'No masks yet'));
            const i = idx(r);
            editor.hidden = i < 0;
            if (i < 0) return;
            const m = masks[i];
            enabled.input.checked = !m.off;
            invert.input.checked = !!m.invert;
            feather.el.hidden = m.type !== 'radial';
            feather.set(m.feather || 0);
            adjust.forEach(({ key, slider }) => slider.set((m.adjust && m.adjust[key]) || 0));
        });
    }
}
