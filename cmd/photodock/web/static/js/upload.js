(function() {
    const CHUNK_SIZE = 1024 * 1024;
    const MAX_CONCURRENT = 3;

    let uploadQueue = [];
    let isUploading = false;
    let activeUploads = 0;

    const uploadZone = document.getElementById('upload-zone');
    const fileInput = document.getElementById('file-input');
    const previewSection = document.getElementById('upload-preview-section');
    const previewGrid = document.getElementById('upload-preview-grid');
    const statusText = document.getElementById('upload-status-text');
    const folderSelect = document.getElementById('upload-folder');
    const startBtn = document.getElementById('start-upload');
    const clearBtn = document.getElementById('clear-upload');

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

    if (previewGrid) {
        previewGrid.addEventListener('click', (e) => {
            const removeBtn = e.target.closest('.remove-btn');
            if (removeBtn) {
                e.preventDefault();
                e.stopPropagation();
                const previewItem = removeBtn.closest('.upload-preview-item');
                if (previewItem && previewItem.dataset.uploadId) {
                    removeFromUpload(previewItem.dataset.uploadId);
                }
            }
        });
    }

    function handleFiles(files) {
        const validFiles = Array.from(files).filter(f =>
            f.type === 'image/jpeg' || f.type === 'image/png'
        );

        if (validFiles.length === 0) {
            alert('Please select valid image files (JPG, PNG)');
            return;
        }

        validFiles.forEach(file => {
            const id = Date.now().toString(36) + Math.random().toString(36).substr(2, 9);
            const item = {
                id,
                file,
                progress: 0,
                status: 'pending',
                error: null,
                previewUrl: null
            };

            uploadQueue.push(item);

            const reader = new FileReader();
            reader.onload = (e) => {
                item.previewUrl = e.target.result;
                addPreviewItem(item);
            };
            reader.readAsDataURL(file);
        });

        updateUI();
    }

    function addPreviewItem(item) {
        const div = document.createElement('div');
        div.className = 'upload-preview-item';
        div.id = `preview-${item.id}`;
        div.dataset.uploadId = item.id;
        div.innerHTML = `
            <img src="${item.previewUrl}" alt="${escapeHtml(item.file.name)}">
            <button class="remove-btn" type="button" title="Remove">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                </svg>
            </button>
            <div class="file-info">${escapeHtml(item.file.name)}</div>
            <div class="progress-overlay">
                <div class="progress-circle"></div>
                <div class="progress-text">0%</div>
            </div>
        `;
        previewGrid.appendChild(div);
    }

    function removeFromUpload(id) {
        const idx = uploadQueue.findIndex(item => item.id === id);
        if (idx === -1) return;

        if (uploadQueue[idx].status !== 'pending') return;

        uploadQueue.splice(idx, 1);
        const element = document.getElementById(`preview-${id}`);
        if (element) {
            element.remove();
        }
        updateUI();
    }

    function updatePreviewItem(id, progress, status) {
        const item = document.getElementById(`preview-${id}`);
        if (!item) return;

        const progressText = item.querySelector('.progress-text');

        item.classList.remove('pending', 'uploading', 'complete', 'error');
        item.classList.add(status);

        if (status === 'uploading') {
            progressText.textContent = `${Math.round(progress)}%`;
        } else if (status === 'complete') {
            progressText.textContent = '✓';
        } else if (status === 'error') {
            progressText.textContent = '✕';
        }
    }

    window.startUpload = function() {
        if (isUploading || uploadQueue.length === 0) return;

        const pending = uploadQueue.filter(i => i.status === 'pending');
        if (pending.length === 0) return;

        isUploading = true;
        if (startBtn) startBtn.disabled = true;
        processQueue();
    };

    window.clearUpload = function() {
        if (isUploading) return;
        uploadQueue = [];
        if (previewGrid) previewGrid.innerHTML = '';
        updateUI();
    };

    function processQueue() {
        const pending = uploadQueue.filter(item => item.status === 'pending');

        if (pending.length === 0 && activeUploads === 0) {
            isUploading = false;
            if (startBtn) startBtn.disabled = false;
            updateUI();

            const complete = uploadQueue.filter(item => item.status === 'complete').length;
            const errors = uploadQueue.filter(item => item.status === 'error').length;

            if (complete > 0) {
                setTimeout(() => {
                    alert(`Upload complete! ${complete} files uploaded${errors > 0 ? `, ${errors} failed` : ''}.`);
                    if (errors === 0) {
                        window.clearUpload();
                    }
                }, 500);
            }
            return;
        }

        while (activeUploads < MAX_CONCURRENT && pending.length > 0) {
            const item = pending.shift();
            item.status = 'uploading';
            updatePreviewItem(item.id, 0, 'uploading');
            activeUploads++;
            uploadFile(item);
        }

        updateUI();
    }

    async function uploadFile(item) {
        const folderId = folderSelect ? folderSelect.value : '';

        try {
            if (item.file.size <= CHUNK_SIZE) {
                await uploadSimple(item, folderId);
            } else {
                await uploadChunked(item, folderId);
            }

            item.status = 'complete';
            item.progress = 100;
            updatePreviewItem(item.id, 100, 'complete');
        } catch (err) {
            item.status = 'error';
            item.error = err.message;
            updatePreviewItem(item.id, item.progress, 'error');
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
                    updatePreviewItem(item.id, progress, 'uploading');
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
            updatePreviewItem(item.id, progress, 'uploading');
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

    function updateUI() {
        const pending = uploadQueue.filter(i => i.status === 'pending').length;
        const uploading = uploadQueue.filter(i => i.status === 'uploading').length;
        const complete = uploadQueue.filter(i => i.status === 'complete').length;
        const errors = uploadQueue.filter(i => i.status === 'error').length;
        const total = uploadQueue.length;

        if (previewSection) {
            previewSection.classList.toggle('has-files', total > 0);
        }

        if (statusText) {
            if (isUploading) {
                statusText.textContent = `Uploading ${uploading} of ${total - complete - errors}...`;
            } else if (total === 0) {
                statusText.textContent = 'No files selected';
            } else if (complete > 0 || errors > 0) {
                statusText.textContent = `${complete} uploaded, ${errors} failed, ${pending} pending`;
            } else {
                statusText.textContent = `${pending} files ready to upload`;
            }
        }

        if (startBtn) {
            startBtn.disabled = isUploading || pending === 0;
        }
        if (clearBtn) {
            clearBtn.disabled = isUploading || total === 0;
        }
    }

    function escapeHtml(str) {
        return str.replace(/[&<>"']/g, c => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
        })[c]);
    }

    updateUI();
})();