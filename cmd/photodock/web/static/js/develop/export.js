import { api } from './api.js';

const OPTS_KEY = 'photodock-develop-export';

// setupExport wires the export dialog: it starts a server job for the current
// (possibly unsaved) recipe, polls it and downloads the file when done.
export function setupExport(button, dialog, { id, getRecipe, toast, showError }) {
    const q = (s) => dialog.querySelector(s);
    const status = q('#dv-ex-status');
    const go = q('#dv-ex-go');
    let polling = false;

    const syncQuality = () => { q('#dv-ex-quality-wrap').hidden = q('#dv-ex-format').value !== 'jpeg'; };
    q('#dv-ex-format').addEventListener('change', syncQuality);

    function open() {
        try {
            const o = JSON.parse(localStorage.getItem(OPTS_KEY) || 'null');
            if (o) {
                q('#dv-ex-format').value = o.format || 'jpeg';
                q('#dv-ex-size').value = o.long_side || '';
                q('#dv-ex-quality').value = o.quality || 92;
                q('#dv-ex-space').value = o.space || 'srgb';
                q('#dv-ex-sharpen').value = o.sharpen || 'none';
                q('#dv-ex-meta').checked = o.metadata !== false;
            }
        } catch (e) {
            status.textContent = '';
        }
        syncQuality();
        if (!polling) status.textContent = '';
        dialog.showModal();
    }

    function options() {
        return {
            format: q('#dv-ex-format').value,
            long_side: Number(q('#dv-ex-size').value) || 0,
            quality: Number(q('#dv-ex-quality').value) || 92,
            space: q('#dv-ex-space').value,
            sharpen: q('#dv-ex-sharpen').value,
            metadata: q('#dv-ex-meta').checked,
        };
    }

    async function poll(jobId) {
        polling = true;
        go.disabled = true;
        try {
            for (;;) {
                await new Promise((r) => setTimeout(r, 1000));
                const job = await api.job(jobId);
                if (job.state === 'done') {
                    status.textContent = `Done: ${job.filename} (${(job.size / 1048576).toFixed(1)} MB)`;
                    const a = document.createElement('a');
                    a.href = `/admin/api/jobs/${jobId}/download`;
                    a.download = job.filename;
                    document.body.appendChild(a);
                    a.click();
                    a.remove();
                    toast(`Exported ${job.filename}`);
                    if (dialog.open) dialog.close();
                    return;
                }
                if (job.state === 'failed') {
                    status.textContent = `Export failed: ${job.error}`;
                    return;
                }
                status.textContent = job.state === 'running' ? 'Rendering full resolution…' : 'Waiting for the render queue…';
            }
        } catch (err) {
            status.textContent = `Export failed: ${err.message}`;
        } finally {
            polling = false;
            go.disabled = false;
        }
    }

    go.addEventListener('click', async () => {
        if (polling) return;
        const opts = options();
        try { localStorage.setItem(OPTS_KEY, JSON.stringify(opts)); } catch (e) { status.textContent = ''; }
        status.textContent = 'Starting…';
        try {
            const job = await api.exportStart(id, getRecipe(), opts);
            poll(job.id);
        } catch (err) {
            status.textContent = '';
            showError(`Export failed: ${err.message}`, false);
        }
    });
    q('#dv-ex-cancel').addEventListener('click', () => dialog.close());
    button.addEventListener('click', open);
    return { open };
}
