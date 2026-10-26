(function () {
    const CHUNK_SIZE = 1024 * 1024;
    const MAX_CONCURRENT = 3;

    const ICON_CLOSE = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';
    const ICON_PREV = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>';
    const ICON_NEXT = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>';
    const ICON_TRASH = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg>';
    const ICON_ROTATE_CCW = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/></svg>';
    const ICON_DEVELOP = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="4" y1="21" x2="4" y2="14"/><line x1="4" y1="10" x2="4" y2="3"/><line x1="12" y1="21" x2="12" y2="12"/><line x1="12" y1="8" x2="12" y2="3"/><line x1="20" y1="21" x2="20" y2="16"/><line x1="20" y1="12" x2="20" y2="3"/><line x1="1" y1="14" x2="7" y2="14"/><line x1="9" y1="8" x2="15" y2="8"/><line x1="17" y1="16" x2="23" y2="16"/></svg>';
    const ICON_ROTATE_CW = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>';

    const uploadZone = document.getElementById('upload-zone');
    const fileInput = document.getElementById('file-input');
    const previewSection = document.getElementById('upload-preview-section');
    const previewGrid = document.getElementById('upload-preview-grid');
    const statusText = document.getElementById('upload-status-text');
    const folderSelect = document.getElementById('upload-folder');
    const startBtn = document.getElementById('start-upload');
    const clearBtn = document.getElementById('clear-upload');

    if (!uploadZone) return;

    let items = [];
    let queue = [];
    let seqCounter = 0;
    let active = 0;
    let committing = false;
    let rotating = false;
    let notice = '';
    let viewerEl = null;
    let viewerKey = null;

    function isValid(file) {
        const n = (file.name || '').toLowerCase();
        return file.type === 'image/jpeg' || file.type === 'image/png' ||
            n.endsWith('.jpg') || n.endsWith('.jpeg') || n.endsWith('.png') || n.endsWith('.arw');
    }

    // Extract Files from a drop, falling back to dataTransfer.items (MTP/PTP drags provide neither).
    function collectDroppedFiles(dt) {
        if (dt.files && dt.files.length) return Array.from(dt.files);
        const out = [];
        if (dt.items) {
            for (const it of dt.items) {
                if (it.kind === 'file') {
                    const f = it.getAsFile();
                    if (f) out.push(f);
                }
            }
        }
        return out;
    }

    function isRawName(name) {
        return name.toLowerCase().endsWith('.arw');
    }

    function formatFileSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    }

    function escapeHtml(str) {
        return String(str).replace(/[&<>"']/g, c => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
        })[c]);
    }

    function statusLabel(item) {
        if (item.status === 'pending') return 'queued';
        if (item.status === 'uploading') return 'uploading ' + Math.round(item.progress) + '%';
        if (item.status === 'error') return 'failed';
        return 'staged';
    }

    function fromServer(it) {
        return {
            key: 'srv-' + it.id,
            id: it.id,
            seq: it.seq,
            filename: it.filename,
            size: it.size,
            width: it.width,
            height: it.height,
            isRaw: it.is_raw,
            rotation: it.rotation || 0,
            edited: !!it.edited,
            editRev: it.edit_rev || 0,
            status: 'complete',
            progress: 100,
            previewUrl: it.preview_url,
            viewUrl: it.view_url,
            objectUrl: null,
            file: null
        };
    }

    async function loadStaged() {
        try {
            const res = await fetch('/admin/staging');
            const data = await res.json();
            items = (data.items || []).map(fromServer);
            seqCounter = items.reduce((m, it) => Math.max(m, it.seq), 0);
        } catch (e) {
            items = [];
        }
        render();
        returnFromEditor();
    }

    // Returning from the editor (#staged=<id>) reopens the file and refreshes until previews re-render.
    function returnFromEditor() {
        const m = location.hash.match(/^#staged=([0-9]+-[0-9a-f]+)$/);
        if (m) {
            history.replaceState(null, '', location.pathname + location.search);
            const item = items.find(i => i.id === m[1]);
            if (item) openViewer(item.key);
        }
        if (m || items.some(i => i.edited)) {
            [1500, 4000, 8000].forEach(ms => setTimeout(refreshStaged, ms));
        }
    }

    async function refreshStaged() {
        if (committing) return;
        let data;
        try {
            const res = await fetch('/admin/staging', { cache: 'no-store' });
            if (!res.ok) return;
            data = await res.json();
        } catch (e) {
            return;
        }
        let changed = false;
        (data.items || []).forEach(it => {
            const item = items.find(i => i.id === it.id);
            if (!item || (item.editRev === (it.edit_rev || 0) && item.edited === !!it.edited)) return;
            Object.assign(item, {
                width: it.width,
                height: it.height,
                rotation: it.rotation || 0,
                edited: !!it.edited,
                editRev: it.edit_rev || 0,
                previewUrl: it.preview_url,
                viewUrl: it.view_url,
            });
            changed = true;
        });
        if (changed) render();
    }

    function developURL(item) {
        return '/admin/develop/staged/' + encodeURIComponent(item.id);
    }

    function alreadyStaged(file) {
        return items.some(i => i.filename === file.name && i.size === file.size);
    }

    function handleFiles(fileList) {
        const files = Array.from(fileList).filter(isValid);
        if (files.length === 0) {
            alert('Please select JPG, PNG or ARW files');
            return;
        }
        let skipped = 0;
        files.forEach(file => {
            if (alreadyStaged(file)) {
                skipped++;
                return;
            }
            const seq = ++seqCounter;
            const key = 'loc-' + seq + '-' + Math.random().toString(36).slice(2, 8);
            items.push({
                key: key,
                id: null,
                seq: seq,
                filename: file.name,
                size: file.size,
                width: 0,
                height: 0,
                isRaw: isRawName(file.name),
                rotation: 0,
                status: 'pending',
                progress: 0,
                previewUrl: null,
                viewUrl: null,
                objectUrl: isRawName(file.name) ? null : URL.createObjectURL(file),
                file: file
            });
            queue.push(key);
        });
        notice = skipped > 0 ? skipped + ' already staged, skipped' : '';
        render();
        pump();
    }

    function pump() {
        while (active < MAX_CONCURRENT && queue.length > 0) {
            const key = queue.shift();
            const item = items.find(i => i.key === key);
            if (!item || item.status !== 'pending') continue;
            item.status = 'uploading';
            active++;
            updateTile(item);
            uploadItem(item).finally(() => {
                active--;
                pump();
                updateUI();
            });
        }
        updateUI();
    }

    async function uploadItem(item) {
        try {
            let result;
            if (item.file.size <= CHUNK_SIZE) {
                result = await uploadSimple(item);
            } else {
                result = await uploadChunked(item);
            }
            item.id = result.id;
            item.seq = result.seq;
            item.width = result.width;
            item.height = result.height;
            item.isRaw = result.is_raw;
            item.rotation = result.rotation || 0;
            item.edited = !!result.edited;
            item.previewUrl = result.preview_url;
            item.viewUrl = result.view_url;
            item.status = 'complete';
            item.progress = 100;
            item.file = null;
            if (item.objectUrl) { URL.revokeObjectURL(item.objectUrl); item.objectUrl = null; }
        } catch (err) {
            item.status = 'error';
        }
        render();
    }

    function uploadSimple(item) {
        const form = new FormData();
        form.append('file', item.file);
        form.append('seq', item.seq);
        return new Promise((resolve, reject) => {
            const xhr = new XMLHttpRequest();
            xhr.upload.onprogress = (e) => {
                if (e.lengthComputable) {
                    item.progress = (e.loaded / e.total) * 100;
                    updateTile(item);
                }
            };
            xhr.onload = () => {
                if (xhr.status >= 200 && xhr.status < 300) {
                    try { resolve(JSON.parse(xhr.responseText)); }
                    catch (e) { reject(new Error('bad response')); }
                } else {
                    reject(new Error(xhr.statusText || 'upload failed'));
                }
            };
            xhr.onerror = () => reject(new Error('network error'));
            xhr.open('POST', '/admin/staging/file');
            xhr.send(form);
        });
    }

    async function uploadChunked(item) {
        const total = Math.ceil(item.file.size / CHUNK_SIZE);
        const initRes = await fetch('/admin/staging/init', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ filename: item.filename, size: item.file.size, seq: item.seq })
        });
        if (!initRes.ok) throw new Error('init failed');
        const { upload_id } = await initRes.json();

        for (let i = 0; i < total; i++) {
            const start = i * CHUNK_SIZE;
            const chunk = item.file.slice(start, Math.min(start + CHUNK_SIZE, item.file.size));
            const form = new FormData();
            form.append('upload_id', upload_id);
            form.append('chunk_index', i);
            form.append('chunk', chunk);
            const res = await fetch('/admin/staging/chunk', { method: 'POST', body: form });
            if (!res.ok) throw new Error('chunk failed');
            item.progress = ((i + 1) / total) * 100;
            updateTile(item);
        }

        const finRes = await fetch('/admin/staging/finalize', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ upload_id })
        });
        if (!finRes.ok) throw new Error('finalize failed');
        return finRes.json();
    }

    async function removeItem(key) {
        const idx = items.findIndex(i => i.key === key);
        if (idx === -1) return;
        const item = items[idx];
        if (item.status === 'uploading') return;
        if (item.id) {
            try { await fetch('/admin/staging/' + item.id, { method: 'DELETE' }); } catch (e) {}
        } else {
            queue = queue.filter(k => k !== key);
        }
        if (item.objectUrl) URL.revokeObjectURL(item.objectUrl);
        items.splice(idx, 1);
        render();
    }

    async function clearAll() {
        if (committing) return;
        const removable = items.filter(i => i.status !== 'uploading');
        for (const it of removable) {
            if (it.id) {
                try { await fetch('/admin/staging/' + it.id, { method: 'DELETE' }); } catch (e) {}
            }
            if (it.objectUrl) URL.revokeObjectURL(it.objectUrl);
        }
        items = items.filter(i => i.status === 'uploading');
        queue = [];
        notice = '';
        closeViewer();
        render();
    }

    function commitAll() {
        if (committing) return;
        const ready = items.filter(i => i.status === 'complete');
        if (ready.length === 0) return;
        if (active > 0 && !confirm('Some files are still uploading and will be skipped. Continue?')) return;

        committing = true;
        closeViewer();
        updateUI();

        const form = new FormData();
        const folderId = folderSelect ? folderSelect.value : '';
        if (folderId) form.append('folder_id', folderId);

        fetch('/admin/staging/commit', { method: 'POST', body: form })
            .then(r => {
                if (!r.ok) throw new Error('commit failed');
                pollCommit();
            })
            .catch(() => {
                committing = false;
                updateUI();
                alert('Failed to start commit');
            });
    }

    function pollCommit() {
        fetch('/admin/staging')
            .then(r => r.json())
            .then(data => {
                const remaining = (data.items || []).length;
                if (statusText) statusText.textContent = 'Adding to gallery… ' + remaining + ' remaining';
                if (remaining === 0) {
                    window.location = '/admin/photos';
                } else {
                    setTimeout(pollCommit, 1500);
                }
            })
            .catch(() => setTimeout(pollCommit, 2000));
    }

    function tileMarkup(item) {
        const ar = item.width && item.height ? (item.width / item.height) : 1;
        const h = ar > 1 ? 120 : Math.min(180, 120 / ar);
        let media;
        if (item.previewUrl) {
            media = '<img src="' + item.previewUrl + '" alt="' + escapeHtml(item.filename) + '">';
        } else if (item.objectUrl) {
            media = '<img src="' + item.objectUrl + '" alt="' + escapeHtml(item.filename) + '">';
        } else {
            media = '<div class="preview-placeholder">RAW</div>';
        }
        const badges = '<div class="tile-badges">' +
            (item.isRaw ? '<span class="raw-badge">RAW</span>' : '') +
            (item.edited ? '<span class="edit-badge" title="Edited">✎</span>' : '') +
            '</div>';
        const meta = (item.width && item.height ? item.width + '×' + item.height + ' • ' : '') + formatFileSize(item.size);
        return badges +
            '<span class="status-badge" title="' + statusLabel(item) + '"></span>' +
            '<div class="preview-image-wrapper" style="height:' + h + 'px;">' + media + '</div>' +
            (item.id && item.status === 'complete'
                ? '<button class="develop-btn" type="button" title="Develop">' + ICON_DEVELOP + '</button>'
                : '') +
            '<button class="remove-btn" type="button" title="Remove">' +
            '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">' +
            '<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg></button>' +
            '<div class="file-info"><span class="file-name-text">' + escapeHtml(item.filename) + '</span>' +
            '<span class="file-meta">' + meta + '</span></div>' +
            '<div class="progress-overlay"><div class="progress-circle"></div>' +
            '<div class="progress-text">0%</div></div>';
    }

    function applyStatusClass(el, item) {
        el.classList.remove('pending', 'uploading', 'complete', 'error', 'image-loaded');
        el.classList.add(item.status);
        if (item.previewUrl || item.objectUrl) el.classList.add('image-loaded');
        const badge = el.querySelector('.status-badge');
        if (badge) badge.title = statusLabel(item);
        const pt = el.querySelector('.progress-text');
        if (pt) pt.textContent = item.status === 'uploading' ? Math.round(item.progress) + '%' : '';
    }

    function updateTile(item) {
        const el = previewGrid.querySelector('[data-key="' + item.key + '"]');
        if (el) applyStatusClass(el, item);
    }

    function render() {
        if (!previewGrid) return;
        items.sort((a, b) => a.seq - b.seq);
        previewGrid.innerHTML = '';
        items.forEach(item => {
            const div = document.createElement('div');
            div.className = 'upload-preview-item';
            div.dataset.key = item.key;
            div.innerHTML = tileMarkup(item);
            previewGrid.appendChild(div);
            applyStatusClass(div, item);
        });
        updateUI();
        if (isViewerOpen()) renderViewer();
    }

    function updateUI() {
        const total = items.length;
        const uploading = items.filter(i => i.status === 'uploading').length;
        const ready = items.filter(i => i.status === 'complete').length;
        const errors = items.filter(i => i.status === 'error').length;

        if (previewSection) previewSection.classList.toggle('has-files', total > 0);

        if (statusText && !committing) {
            let text;
            if (total === 0) text = 'No files staged';
            else if (uploading > 0) text = 'Uploading ' + uploading + '…, ' + ready + ' staged';
            else text = ready + ' staged' + (errors ? ', ' + errors + ' failed' : '');
            if (notice) text += ' · ' + notice;
            statusText.textContent = text;
        }

        if (startBtn) startBtn.disabled = committing || ready === 0;
        if (clearBtn) clearBtn.disabled = committing || total === 0;
    }

    function isViewerOpen() {
        return !!viewerEl && viewerEl.classList.contains('open');
    }

    function viewerIdx() {
        return items.findIndex(i => i.key === viewerKey);
    }

    function buildViewer() {
        if (viewerEl) return viewerEl;
        viewerEl = document.createElement('div');
        viewerEl.className = 'stage-viewer';
        viewerEl.innerHTML =
            '<div class="stage-viewer-header">' +
            '<div class="stage-viewer-title">' +
            '<span class="stage-viewer-name"></span>' +
            '<span class="stage-viewer-meta"></span>' +
            '</div>' +
            '<div class="stage-viewer-actions">' +
            '<span class="stage-viewer-counter"></span>' +
            '<button class="btn-icon stage-viewer-develop" type="button" title="Develop (E)">' + ICON_DEVELOP + '</button>' +
            '<button class="btn-icon stage-viewer-rotate stage-viewer-rotate-ccw" type="button" title="Rotate left (Shift+R)">' + ICON_ROTATE_CCW + '</button>' +
            '<button class="btn-icon stage-viewer-rotate stage-viewer-rotate-cw" type="button" title="Rotate right (R)">' + ICON_ROTATE_CW + '</button>' +
            '<button class="btn-icon stage-viewer-delete" type="button" title="Delete (Del)">' + ICON_TRASH + '</button>' +
            '<button class="btn-icon stage-viewer-close" type="button" title="Close (Esc)">' + ICON_CLOSE + '</button>' +
            '</div>' +
            '</div>' +
            '<div class="stage-viewer-body">' +
            '<button class="stage-viewer-nav stage-viewer-prev" type="button" title="Previous (←)">' + ICON_PREV + '</button>' +
            '<div class="stage-viewer-stage"></div>' +
            '<button class="stage-viewer-nav stage-viewer-next" type="button" title="Next (→)">' + ICON_NEXT + '</button>' +
            '</div>';
        document.body.appendChild(viewerEl);

        viewerEl.querySelector('.stage-viewer-close').addEventListener('click', closeViewer);
        viewerEl.querySelector('.stage-viewer-prev').addEventListener('click', () => step(-1));
        viewerEl.querySelector('.stage-viewer-next').addEventListener('click', () => step(1));
        viewerEl.querySelector('.stage-viewer-delete').addEventListener('click', deleteCurrent);
        viewerEl.querySelector('.stage-viewer-develop').addEventListener('click', developCurrent);
        viewerEl.querySelector('.stage-viewer-rotate-cw').addEventListener('click', () => rotateCurrent('cw'));
        viewerEl.querySelector('.stage-viewer-rotate-ccw').addEventListener('click', () => rotateCurrent('ccw'));
        viewerEl.querySelector('.stage-viewer-body').addEventListener('click', (e) => {
            if (e.target.classList.contains('stage-viewer-body') || e.target.classList.contains('stage-viewer-stage')) {
                closeViewer();
            }
        });
        return viewerEl;
    }

    function openViewer(key) {
        if (items.findIndex(i => i.key === key) === -1) return;
        viewerKey = key;
        buildViewer();
        viewerEl.classList.add('open');
        document.body.style.overflow = 'hidden';
        renderViewer();
    }

    function closeViewer() {
        if (!viewerEl) return;
        viewerEl.classList.remove('open');
        viewerEl.querySelector('.stage-viewer-stage').innerHTML = '';
        viewerKey = null;
        document.body.style.overflow = '';
    }

    function renderViewer() {
        if (!viewerEl) return;
        const idx = viewerIdx();
        const item = items[idx];
        if (!item) {
            closeViewer();
            return;
        }

        const stage = viewerEl.querySelector('.stage-viewer-stage');
        const src = item.viewUrl || item.previewUrl || item.objectUrl;
        if (src) {
            stage.innerHTML = '<img src="' + src + '" alt="' + escapeHtml(item.filename) + '">';
            const img = stage.querySelector('img');
            img.addEventListener('click', () => img.classList.toggle('zoomed'));
            img.addEventListener('error', () => {
                stage.innerHTML = '<div class="stage-viewer-empty">Preview unavailable</div>';
            });
        } else {
            stage.innerHTML = '<div class="stage-viewer-empty">RAW — preview not generated yet</div>';
        }

        const meta = [];
        if (item.width && item.height) meta.push(item.width + '×' + item.height);
        meta.push(formatFileSize(item.size));
        if (item.isRaw) meta.push('RAW');
        if (item.edited) meta.push('edited');
        else if (item.rotation) meta.push(item.rotation + '°');
        meta.push(statusLabel(item));

        const busy = rotating || committing || item.status !== 'complete';
        viewerEl.querySelector('.stage-viewer-name').textContent = item.filename;
        viewerEl.querySelector('.stage-viewer-meta').textContent = meta.join(' • ');
        viewerEl.querySelector('.stage-viewer-counter').textContent = (idx + 1) + ' / ' + items.length;
        viewerEl.querySelector('.stage-viewer-prev').disabled = idx <= 0;
        viewerEl.querySelector('.stage-viewer-next').disabled = idx >= items.length - 1;
        viewerEl.querySelector('.stage-viewer-delete').disabled = item.status === 'uploading' || committing;
        viewerEl.querySelector('.stage-viewer-develop').disabled = busy || !item.id;
        viewerEl.querySelector('.stage-viewer-rotate-cw').disabled = busy || !item.id;
        viewerEl.querySelector('.stage-viewer-rotate-ccw').disabled = busy || !item.id;
        viewerEl.classList.toggle('busy', rotating);
    }

    function step(delta) {
        const idx = viewerIdx();
        const next = items[idx + delta];
        if (!next) return;
        viewerKey = next.key;
        renderViewer();
    }

    async function rotateCurrent(dir) {
        const item = items[viewerIdx()];
        if (!item || !item.id || item.status !== 'complete' || rotating || committing) return;

        rotating = true;
        renderViewer();

        const body = new FormData();
        body.append('dir', dir);
        try {
            const res = await fetch('/admin/staging/' + item.id + '/rotate', { method: 'POST', body });
            if (!res.ok) throw new Error('rotate failed');
            const data = await res.json();
            item.rotation = data.rotation;
            item.width = data.width;
            item.height = data.height;
            item.edited = !!data.edited;
            item.previewUrl = data.preview_url;
            item.viewUrl = data.view_url;
        } catch (e) {
            alert('Failed to rotate ' + item.filename);
        } finally {
            rotating = false;
            render();
        }
    }

    // developCurrent opens the full editor for a file still in Unsorted.
    function developCurrent() {
        const item = items[viewerIdx()];
        if (!item || !item.id || item.status !== 'complete' || committing) return;
        window.location.href = developURL(item);
    }

    async function deleteCurrent() {
        const idx = viewerIdx();
        const item = items[idx];
        if (!item || item.status === 'uploading' || committing) return;
        const fallback = items[idx + 1] || items[idx - 1];
        viewerKey = fallback ? fallback.key : null;
        await removeItem(item.key);
        if (!viewerKey || items.length === 0) closeViewer();
        else renderViewer();
    }

    document.addEventListener('keydown', (e) => {
        if (!isViewerOpen()) return;
        if (e.key === 'Escape') {
            const img = viewerEl.querySelector('.stage-viewer-stage img.zoomed');
            if (img) img.classList.remove('zoomed');
            else closeViewer();
        } else if (e.key === 'ArrowLeft') {
            step(-1);
        } else if (e.key === 'ArrowRight') {
            step(1);
        } else if (e.key === 'Delete') {
            e.preventDefault();
            deleteCurrent();
        } else if (e.key === 'e' || e.key === 'E') {
            developCurrent();
        } else if (e.key === 'r') {
            rotateCurrent('cw');
        } else if (e.key === 'R') {
            rotateCurrent('ccw');
        }
    });

    uploadZone.addEventListener('click', () => fileInput.click());
    uploadZone.addEventListener('dragover', (e) => { e.preventDefault(); uploadZone.classList.add('dragover'); });
    uploadZone.addEventListener('dragleave', () => uploadZone.classList.remove('dragover'));
    uploadZone.addEventListener('drop', (e) => {
        e.preventDefault();
        uploadZone.classList.remove('dragover');
        const dropped = collectDroppedFiles(e.dataTransfer);
        if (dropped.length === 0) {
            alert('No files found in that drop. Dragging directly from a camera or phone (MTP/PTP) doesn’t expose the files to the browser — click this box to pick them with the file dialog, or copy the photos to a folder on disk first.');
            return;
        }
        handleFiles(dropped);
    });
    fileInput.addEventListener('change', () => { handleFiles(fileInput.files); fileInput.value = ''; });

    if (previewGrid) {
        previewGrid.addEventListener('click', (e) => {
            const tile = e.target.closest('.upload-preview-item');
            if (!tile) return;
            const dev = e.target.closest('.develop-btn');
            if (dev) {
                e.preventDefault();
                e.stopPropagation();
                const it = items.find(i => i.key === tile.dataset.key);
                if (it && it.id && !committing) window.location.href = developURL(it);
                return;
            }
            const btn = e.target.closest('.remove-btn');
            if (btn) {
                e.preventDefault();
                e.stopPropagation();
                removeItem(tile.dataset.key);
                return;
            }
            const item = items.find(i => i.key === tile.dataset.key);
            if (!item || item.status === 'uploading') return;
            openViewer(tile.dataset.key);
        });
        previewGrid.addEventListener('error', (e) => {
            const img = e.target;
            if (img && img.tagName === 'IMG' && img.closest('.upload-preview-item')) {
                const wrap = img.closest('.preview-image-wrapper');
                if (wrap) wrap.innerHTML = '<div class="preview-placeholder">RAW</div>';
            }
        }, true);
    }

    if (startBtn) startBtn.addEventListener('click', commitAll);
    if (clearBtn) clearBtn.addEventListener('click', clearAll);

    loadStaged();
})();