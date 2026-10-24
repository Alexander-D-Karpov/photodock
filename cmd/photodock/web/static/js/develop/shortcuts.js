export function bindShortcuts(map) {
    document.addEventListener('keydown', (e) => {
        const t = e.target;
        const tag = t && t.tagName;
        const isRange = tag === 'INPUT' && t.type === 'range';
        const typing = (tag === 'INPUT' && !isRange) || tag === 'TEXTAREA' || tag === 'SELECT' || (t && t.isContentEditable);
        if (typing && e.key !== 'Escape') return;
        if (isRange && e.key.startsWith('Arrow')) return;
        if (e.altKey) return;
        if (document.querySelector('dialog[open]')) return;
        const key = (e.ctrlKey || e.metaKey ? 'Mod+' : '') + e.key;
        const fn = map[key];
        if (!fn) return;
        e.preventDefault();
        fn(e);
    });
}
