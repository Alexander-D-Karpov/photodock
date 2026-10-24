import { api } from './api.js';

export class PresetsPanel {
    constructor(list, { onApply, onError }) {
        this.list = list;
        this.cb = { onApply, onError };
        this.items = [];
    }

    async load() {
        try {
            const { items } = await api.presets();
            this.items = items || [];
            this.render();
        } catch (err) {
            this.cb.onError(err);
        }
    }

    render() {
        this.list.innerHTML = '';
        const groups = new Map();
        this.items.forEach((p) => {
            const g = p.builtin ? p.group || 'Built-in' : 'My presets';
            if (!groups.has(g)) groups.set(g, []);
            groups.get(g).push(p);
        });
        if (!groups.has('My presets')) groups.set('My presets', []);
        groups.forEach((items, name) => {
            const head = document.createElement('li');
            head.className = 'dv-preset-group';
            head.textContent = name;
            this.list.appendChild(head);
            if (!items.length) {
                const li = document.createElement('li');
                li.className = 'dv-empty';
                li.textContent = 'Save one with “Save preset…”';
                this.list.appendChild(li);
                return;
            }
            items.forEach((p) => {
                const li = document.createElement('li');
                const apply = document.createElement('button');
                apply.type = 'button';
                apply.className = 'dv-preset-apply';
                apply.textContent = p.name;
                apply.title = `Applies: ${p.modules.join(', ')}`;
                apply.addEventListener('click', () => this.cb.onApply(p));
                li.appendChild(apply);
                if (!p.builtin) {
                    const del = document.createElement('button');
                    del.type = 'button';
                    del.className = 'dv-preset-del';
                    del.textContent = '×';
                    del.title = 'Delete preset';
                    del.addEventListener('click', async () => {
                        if (!confirm(`Delete preset “${p.name}”?`)) return;
                        try {
                            await api.deletePreset(p.id);
                            this.load();
                        } catch (err) {
                            this.cb.onError(err);
                        }
                    });
                    li.appendChild(del);
                }
                this.list.appendChild(li);
            });
        });
    }
}
