import { MODULE_GROUPS } from './recipe.js';

// chooseModules asks which recipe modules to paste or save; it resolves to
// { name, modules } or null when cancelled.
export function chooseModules({ title, okLabel = 'OK', withName = false, selected = [] }) {
    const dialog = document.getElementById('dv-modules-dialog');
    const q = (id) => dialog.querySelector(id);
    const list = q('#dv-md-list');
    const name = q('#dv-md-name');
    q('#dv-md-title').textContent = title;
    q('#dv-md-ok').textContent = okLabel;
    q('#dv-md-name-wrap').hidden = !withName;
    name.value = '';
    list.innerHTML = '';
    const boxes = MODULE_GROUPS.map(([id, label]) => {
        const wrap = document.createElement('label');
        wrap.className = 'dv-check';
        const box = document.createElement('input');
        box.type = 'checkbox';
        box.value = id;
        box.checked = selected.includes(id);
        wrap.append(box, document.createTextNode(` ${label}`));
        list.appendChild(wrap);
        return box;
    });

    return new Promise((resolve) => {
        const finish = (value) => {
            dialog.oncancel = null;
            if (dialog.open) dialog.close();
            resolve(value);
        };
        q('#dv-md-all').onclick = () => boxes.forEach((b) => { b.checked = true; });
        q('#dv-md-none').onclick = () => boxes.forEach((b) => { b.checked = false; });
        q('#dv-md-cancel').onclick = () => finish(null);
        dialog.oncancel = () => finish(null);
        q('#dv-md-ok').onclick = () => {
            const modules = boxes.filter((b) => b.checked).map((b) => b.value);
            if (!modules.length) return;
            if (withName && !name.value.trim()) {
                name.focus();
                return;
            }
            finish({ name: name.value.trim(), modules });
        };
        dialog.showModal();
        if (withName) name.focus();
    });
}
