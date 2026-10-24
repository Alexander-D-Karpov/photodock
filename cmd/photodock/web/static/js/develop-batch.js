// Batch develop actions on admin/photos: auto, apply preset, paste copied
// settings and reset edits for the selected photos (selectedPhotos is the
// global Set from admin.js).
(function () {
    // Module ids must match internal/develop/modules.go.
    const MODULES = [
        ['wb', 'White balance'], ['exposure', 'Exposure'], ['tone', 'Tone'], ['curve', 'Tone curve'],
        ['local', 'Local contrast'], ['presence', 'Vibrance & saturation'], ['hsl', 'HSL'],
        ['grading', 'Color grading'], ['bw', 'Black & white'], ['mixer', 'Channel mixer'],
        ['sharpen', 'Sharpening'], ['noise', 'Noise reduction'], ['lens', 'Lens correction'],
        ['raw', 'RAW decoding'], ['crop', 'Crop & rotate'], ['masks', 'Masks'],
    ];
    const DEFAULT_PASTE = MODULES.map((m) => m[0]).filter((id) => id !== 'crop' && id !== 'masks');
    const CLIPBOARD_KEY = 'photodock-develop-clipboard';
    const PASTE_MODULES_KEY = 'photodock-develop-paste-modules';

    const dialog = document.getElementById('batch-dialog');
    if (!dialog) return;
    const q = (id) => document.getElementById(id);
    const list = q('batch-modules');
    const presetSel = q('batch-preset');
    const status = q('batch-status');
    const okBtn = q('batch-ok');

    let mode = 'preset';
    let presets = [];
    let clipboard = null;

    const boxes = MODULES.map(([id, label]) => {
        const wrap = document.createElement('label');
        const box = document.createElement('input');
        box.type = 'checkbox';
        box.value = id;
        wrap.append(box, document.createTextNode(label));
        list.appendChild(wrap);
        return box;
    });

    function check(ids) {
        boxes.forEach((b) => { b.checked = ids.includes(b.value); });
    }

    function storedPasteModules() {
        try {
            const v = JSON.parse(localStorage.getItem(PASTE_MODULES_KEY) || 'null');
            if (Array.isArray(v) && v.length) return v;
        } catch (e) {
            return DEFAULT_PASTE;
        }
        return DEFAULT_PASTE;
    }

    function selectedIds() {
        return Array.from(selectedPhotos);
    }

    async function open(nextMode) {
        if (!selectedPhotos.size) return;
        mode = nextMode;
        status.textContent = '';
        okBtn.disabled = false;
        q('batch-preset-wrap').hidden = mode !== 'preset';
        if (mode === 'preset') {
            q('batch-title').textContent = `Apply preset to ${selectedPhotos.size} photos`;
            const res = await fetch('/admin/api/presets', { cache: 'no-store' });
            presets = res.ok ? (await res.json()).items || [] : [];
            if (!presets.length) {
                alert('No presets available.');
                return;
            }
            presetSel.innerHTML = '';
            presets.forEach((p, i) => {
                const o = document.createElement('option');
                o.value = String(i);
                o.textContent = p.builtin ? `${p.group} · ${p.name}` : p.name;
                presetSel.appendChild(o);
            });
            check(presets[0].modules);
        } else {
            try {
                clipboard = JSON.parse(localStorage.getItem(CLIPBOARD_KEY) || 'null');
            } catch (e) {
                clipboard = null;
            }
            if (!clipboard) {
                alert('Nothing to paste. Copy settings in the editor first (Ctrl+C).');
                return;
            }
            q('batch-title').textContent = `Paste settings to ${selectedPhotos.size} photos`;
            check(storedPasteModules());
        }
        dialog.showModal();
    }

    presetSel.addEventListener('change', () => {
        const p = presets[Number(presetSel.value)];
        if (p) check(p.modules);
    });

    q('batch-cancel').addEventListener('click', () => dialog.close());

    okBtn.addEventListener('click', async () => {
        const modules = boxes.filter((b) => b.checked).map((b) => b.value);
        if (!modules.length) {
            status.textContent = 'Select at least one module.';
            return;
        }
        let recipe = clipboard;
        let label = 'Batch paste';
        if (mode === 'preset') {
            const p = presets[Number(presetSel.value)];
            recipe = p.recipe;
            label = `Preset: ${p.name}`;
        } else {
            try {
                localStorage.setItem(PASTE_MODULES_KEY, JSON.stringify(modules));
            } catch (e) {
                status.textContent = '';
            }
        }
        okBtn.disabled = true;
        status.textContent = 'Applying…';
        try {
            const res = await fetch('/admin/api/develop/batch', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ ids: selectedIds(), recipe, modules, label }),
            });
            if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
            const r = await res.json();
            let msg = `Applied to ${r.applied}, unchanged ${r.unchanged}`;
            if (r.failed.length) msg += `, failed ${r.failed.length}: ${r.failed.map((f) => `#${f.photo_id} ${f.error}`).join('; ')}`;
            alert(msg);
            location.reload();
        } catch (err) {
            status.textContent = `Failed: ${err.message}`;
            okBtn.disabled = false;
        }
    });

    window.batchApplyPreset = () => open('preset');
    window.batchPaste = () => open('paste');
    window.batchResetEdits = async () => {
        if (!selectedPhotos.size) return;
        if (!confirm(`Discard all edits on ${selectedPhotos.size} photos?`)) return;
        const res = await fetch('/admin/api/develop/batch-reset', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ids: selectedIds() }),
        });
        if (!res.ok) {
            alert(`Failed: ${(await res.text()).trim()}`);
            return;
        }
        location.reload();
    };
    window.batchAuto = async () => {
        if (!selectedPhotos.size) return;
        const wb = confirm(`Auto-develop ${selectedPhotos.size} photos.\n\nOK — also auto white balance\nCancel — keep camera white balance`);
        const res = await fetch('/admin/api/develop/batch-auto', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ids: selectedIds(), wb }),
        });
        if (!res.ok) {
            alert(`Failed: ${(await res.text()).trim()}`);
            return;
        }
        alert(`Auto started for ${selectedPhotos.size} photos. Thumbnails update as each one is rendered.`);
    };
})();
