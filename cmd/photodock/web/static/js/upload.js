(function() {
    const CHUNK_SIZE = 1024 * 1024;
    const MAX_CONCURRENT = 3;

    let uploadQueue = [];
    let isUploading = false;
    let activeUploads = 0;

    const uploadZone = document.getElementById('upload-zone');
    const fileInput = document.getElementById('file-input');
    const queueContainer = document.getElementById('upload-queue');
    const queueList = document.getElementById('queue-list');
    const statusEl = document.getElementById('upload-status');
    const folderSelect = document.getElementById('upload-folder');

    if (!uploadZone) return;

    uploadZone.addEventListener('click', () => fileInput.click());

    uploadZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        uploadZone.classList.add('dragover');
    });

    uploadZone.addEventListener('dragleave', () => {
        uploadZone.classList.remove('dragover');
    });

    uploadZone.addEventListener('drop', (e) => {
        e.preventDefault();
        uploadZone.classList.remove('dragover');
        handleFiles(e.dataTransfer.files);
    });

    fileInput.addEventListener('change', () => {
        handleFiles(fileInput.files);
        fileInput.value = '';
    });

    function handleFiles(files) {
        const validFiles = Array.from(files).filter(f =>
            f.type === 'image/jpeg' || f.type === 'image/png'
        );

        if (validFiles.length === 0) {
            alert('Please select valid image files (JPG, PNG)');
            return;
        }

        validFiles.forEach(file => {
            const id = Date.now() + Math.random().toString(36).substr(2, 9);
            uploadQueue.push({
                id,
                file,
                progress: 0,
                status: 'pending',
                error: null
            });
            addQueueItem(id, file);
        });

        queueContainer.style.display = 'block';
        updateStatus();
    }

    function addQueueItem(id, file) {
        const item = document.createElement('div');
        item.className = 'queue-item';
        item.id = `queue-${id}`;
        item.innerHTML = `
            <div class="queue-item-info">
                <span class="queue-item-name">${escapeHtml(file.name)}</span>
                <span class="queue-item-size">${formatSize(file.size)}</span>
            </div>
            <div class="queue-item-progress">
                <div class="progress-bar">
                    <div class="progress-fill" style="width: 0%"></div>
                </div>
                <span class="progress-text">0%</span>
            </div>
            <div class="queue-item-status">
                <span class="status-text">Pending</span>
                <button class="btn-icon remove-btn" onclick="removeFromQueue('${id}')" title="Remove">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
                </button>
            </div>
        `;
        queueList.appendChild(item);
    }

    function updateQueueItem(id, progress, status, error) {
        const item = document.getElementById(`queue-${id}`);
        if (!item) return;

        const progressFill = item.querySelector('.progress-fill');
        const progressText = item.querySelector('.progress-text');
        const statusText = item.querySelector('.status-text');

        progressFill.style.width = `${progress}%`;
        progressText.textContent = `${Math.round(progress)}%`;

        item.className = `queue-item ${status}`;

        if (status === 'uploading') {
            statusText.textContent = 'Uploading...';
        } else if (status === 'complete') {
            statusText.textContent = 'Complete';
            progressFill.style.background = 'var(--success)';
        } else if (status === 'error') {
            statusText.textContent = error || 'Error';
            progressFill.style.background = 'var(--danger)';
        }
    }

    window.removeFromQueue = function(id) {
        const idx = uploadQueue.findIndex(item => item.id === id);
        if (idx !== -1 && uploadQueue[idx].status === 'pending') {
            uploadQueue.splice(idx, 1);
            document.getElementById(`queue-${id}`)?.remove();
            updateStatus();
        }
    };

    window.startUpload = function() {
        if (isUploading) return;
        isUploading = true;
        document.getElementById('start-upload').disabled = true;
        processQueue();
    };

    window.clearQueue = function() {
        if (isUploading) return;
        uploadQueue = [];
        queueList.innerHTML = '';
        queueContainer.style.display = 'none';
        updateStatus();
    };

    function processQueue() {
        const pending = uploadQueue.filter(item => item.status === 'pending');

        if (pending.length === 0 && activeUploads === 0) {
            isUploading = false;
            document.getElementById('start-upload').disabled = false;
            updateStatus();

            const complete = uploadQueue.filter(item => item.status === 'complete').length;
            const errors = uploadQueue.filter(item => item.status === 'error').length;

            if (complete > 0) {
                alert(`Upload complete! ${complete} files uploaded${errors > 0 ? `, ${errors} failed` : ''}.`);
            }
            return;
        }

        while (activeUploads < MAX_CONCURRENT && pending.length > 0) {
            const item = pending.shift();
            item.status = 'uploading';
            activeUploads++;
            uploadFile(item);
        }

        updateStatus();
    }

    async function uploadFile(item) {
        const folderId = folderSelect.value;

        try {
            if (item.file.size <= CHUNK_SIZE) {
                await uploadSimple(item, folderId);
            } else {
                await uploadChunked(item, folderId);
            }

            item.status = 'complete';
            item.progress = 100;
            updateQueueItem(item.id, 100, 'complete');
        } catch (err) {
            item.status = 'error';
            item.error = err.message;
            updateQueueItem(item.id, item.progress, 'error', err.message);
        }

        activeUploads--;
        processQueue();
    }

    async function uploadSimple(item, folderId) {
        const formData = new FormData();
        formData.append('file', item.file);
        if (folderId) formData.append('folder_id', folderId);

        const xhr = new XMLHttpRequest();

        return new Promise((resolve, reject) => {
            xhr.upload.onprogress = (e) => {
                if (e.lengthComputable) {
                    const progress = (e.loaded / e.total) * 100;
                    item.progress = progress;
                    updateQueueItem(item.id, progress, 'uploading');
                }
            };

            xhr.onload = () => {
                if (xhr.status >= 200 && xhr.status < 300) {
                    resolve();
                } else {
                    reject(new Error(xhr.statusText || 'Upload failed'));
                }
            };

            xhr.onerror = () => reject(new Error('Network error'));

            xhr.open('POST', '/admin/upload/file');
            xhr.send(formData);
        });
    }

    async function uploadChunked(item, folderId) {
        const totalChunks = Math.ceil(item.file.size / CHUNK_SIZE);
        const uploadId = await initChunkedUpload(item.file.name, item.file.size, folderId);

        for (let i = 0; i < totalChunks; i++) {
            const start = i * CHUNK_SIZE;
            const end = Math.min(start + CHUNK_SIZE, item.file.size);
            const chunk = item.file.slice(start, end);

            await uploadChunk(uploadId, i, chunk);

            const progress = ((i + 1) / totalChunks) * 100;
            item.progress = progress;
            updateQueueItem(item.id, progress, 'uploading');
        }

        await finalizeUpload(uploadId);
    }

    async function initChunkedUpload(filename, size, folderId) {
        const res = await fetch('/admin/upload/init', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ filename, size, folder_id: folderId || null })
        });

        if (!res.ok) throw new Error('Failed to init upload');
        const data = await res.json();
        return data.upload_id;
    }

    async function uploadChunk(uploadId, index, chunk) {
        const formData = new FormData();
        formData.append('upload_id', uploadId);
        formData.append('chunk_index', index);
        formData.append('chunk', chunk);

        const res = await fetch('/admin/upload/chunk', {
            method: 'POST',
            body: formData
        });

        if (!res.ok) throw new Error('Chunk upload failed');
    }

    async function finalizeUpload(uploadId) {
        const res = await fetch('/admin/upload/finalize', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ upload_id: uploadId })
        });

        if (!res.ok) throw new Error('Failed to finalize upload');
    }

    function updateStatus() {
        const pending = uploadQueue.filter(i => i.status === 'pending').length;
        const uploading = uploadQueue.filter(i => i.status === 'uploading').length;
        const complete = uploadQueue.filter(i => i.status === 'complete').length;
        const errors = uploadQueue.filter(i => i.status === 'error').length;

        if (isUploading) {
            statusEl.textContent = `Uploading ${uploading} of ${uploadQueue.length}...`;
        } else if (complete > 0 || errors > 0) {
            statusEl.textContent = `${complete} complete, ${errors} errors, ${pending} pending`;
        } else {
            statusEl.textContent = `${pending} files ready`;
        }
    }

    function formatSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    }

    function escapeHtml(str) {
        return str.replace(/[&<>"']/g, c => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
        })[c]);
    }
})();