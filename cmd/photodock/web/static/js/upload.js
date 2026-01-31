(function() {
    const CHUNK_SIZE = 1024 * 1024;
    const MAX_CONCURRENT = 3;

    let uploadQueue = [];
    let isUploading = false;
    let activeUploads = 0;
    let previewModalOpen = false;
    let currentPreviewIndex = 0;

    const uploadZone = document.getElementById('upload-zone');
    const fileInput = document.getElementById('file-input');
    const previewSection = document.getElementById('upload-preview-section');
    const previewGrid = document.getElementById('upload-preview-grid');
    const statusText = document.getElementById('upload-status-text');
    const folderSelect = document.getElementById('upload-folder');
    const startBtn = document.getElementById('start-upload');
    const clearBtn = document.getElementById('clear-upload');

    if (!uploadZone) return;

    createPreviewModal();

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
                return;
            }

            const previewItem = e.target.closest('.upload-preview-item');
            if (previewItem && previewItem.dataset.uploadId) {
                const idx = uploadQueue.findIndex(item => item.id === previewItem.dataset.uploadId);
                if (idx !== -1) {
                    openPreviewModal(idx);
                }
            }
        });
    }

    function createPreviewModal() {
        const modal = document.createElement('div');
        modal.className = 'photo-preview-modal';
        modal.id = 'photo-preview-modal';
        modal.innerHTML = `
            <div class="photo-preview-header">
                <span class="file-name"></span>
                <div class="photo-preview-nav">
                    <button class="btn-icon" id="preview-delete-btn" title="Remove from queue">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                        </svg>
                    </button>
                    <button class="btn-icon" id="preview-close-btn" title="Close (Esc)">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                        </svg>
                    </button>
                </div>
            </div>
            <div class="photo-preview-body">
                <button class="photo-preview-prev" id="preview-prev-btn" title="Previous (←)">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <polyline points="15 18 9 12 15 6"/>
                    </svg>
                </button>
                <div class="photo-preview-image-container">
                    <img src="" alt="">
                    <div class="photo-preview-info">
                        <span class="preview-dimensions"></span>
                        <span class="preview-size"></span>
                    </div>
                </div>
                <button class="photo-preview-next" id="preview-next-btn" title="Next (→)">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <polyline points="9 18 15 12 9 6"/>
                    </svg>
                </button>
            </div>
            <div class="photo-preview-footer">
                <div class="photo-preview-counter"></div>
                <div class="photo-preview-actions">
                    <button class="btn btn-danger" id="preview-delete-footer-btn">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:16px;height:16px">
                            <polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                        </svg>
                        Remove
                    </button>
                </div>
            </div>
        `;
        document.body.appendChild(modal);

        const style = document.createElement('style');
        style.textContent = `
            .photo-preview-modal {
                position: fixed;
                inset: 0;
                background: rgba(0, 0, 0, 0.95);
                z-index: 10000;
                display: none;
                flex-direction: column;
            }
            .photo-preview-modal.active {
                display: flex;
            }
            .photo-preview-header {
                display: flex;
                justify-content: space-between;
                align-items: center;
                padding: 15px 20px;
                background: rgba(0, 0, 0, 0.8);
                border-bottom: 1px solid rgba(255,255,255,0.1);
            }
            .photo-preview-header .file-name {
                color: #fff;
                font-size: 1rem;
                font-weight: 500;
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
                flex: 1;
                margin-right: 20px;
            }
            .photo-preview-nav {
                display: flex;
                gap: 8px;
            }
            .photo-preview-nav .btn-icon {
                color: rgba(255,255,255,0.8);
                background: rgba(255,255,255,0.1);
                border-radius: 8px;
                width: 40px;
                height: 40px;
            }
            .photo-preview-nav .btn-icon:hover {
                color: #fff;
                background: rgba(255,255,255,0.2);
            }
            .photo-preview-nav .btn-icon svg {
                width: 20px;
                height: 20px;
            }
            .photo-preview-body {
                flex: 1;
                display: flex;
                align-items: center;
                justify-content: center;
                position: relative;
                overflow: hidden;
                padding: 20px;
            }
            .photo-preview-image-container {
                position: relative;
                max-width: 100%;
                max-height: 100%;
                display: flex;
                flex-direction: column;
                align-items: center;
            }
            .photo-preview-body img {
                max-width: calc(100vw - 200px);
                max-height: calc(100vh - 200px);
                object-fit: contain;
                border-radius: 4px;
                box-shadow: 0 10px 40px rgba(0,0,0,0.5);
            }
            .photo-preview-info {
                margin-top: 15px;
                display: flex;
                gap: 20px;
                color: rgba(255,255,255,0.7);
                font-size: 0.9rem;
            }
            .photo-preview-prev,
            .photo-preview-next {
                position: absolute;
                top: 50%;
                transform: translateY(-50%);
                background: rgba(0, 0, 0, 0.6);
                border: none;
                color: rgba(255,255,255,0.8);
                width: 60px;
                height: 100px;
                cursor: pointer;
                display: flex;
                align-items: center;
                justify-content: center;
                transition: all 0.2s;
                z-index: 10;
            }
            .photo-preview-prev:hover,
            .photo-preview-next:hover {
                background: rgba(0, 0, 0, 0.8);
                color: #fff;
            }
            .photo-preview-prev:disabled,
            .photo-preview-next:disabled {
                opacity: 0.3;
                cursor: not-allowed;
            }
            .photo-preview-prev {
                left: 0;
                border-radius: 0 8px 8px 0;
            }
            .photo-preview-next {
                right: 0;
                border-radius: 8px 0 0 8px;
            }
            .photo-preview-prev svg,
            .photo-preview-next svg {
                width: 32px;
                height: 32px;
            }
            .photo-preview-footer {
                display: flex;
                justify-content: space-between;
                align-items: center;
                padding: 15px 20px;
                background: rgba(0, 0, 0, 0.8);
                border-top: 1px solid rgba(255,255,255,0.1);
            }
            .photo-preview-counter {
                color: rgba(255,255,255,0.7);
                font-size: 0.95rem;
            }
            .photo-preview-actions {
                display: flex;
                gap: 10px;
            }
            .photo-preview-actions .btn {
                padding: 8px 16px;
            }
            @media (max-width: 768px) {
                .photo-preview-body img {
                    max-width: calc(100vw - 40px);
                    max-height: calc(100vh - 180px);
                }
                .photo-preview-prev,
                .photo-preview-next {
                    width: 50px;
                    height: 80px;
                }
            }
        `;
        document.head.appendChild(style);

        document.getElementById('preview-close-btn').addEventListener('click', closePreviewModal);
        document.getElementById('preview-prev-btn').addEventListener('click', () => navigatePreview(-1));
        document.getElementById('preview-next-btn').addEventListener('click', () => navigatePreview(1));
        document.getElementById('preview-delete-btn').addEventListener('click', deleteCurrentPreview);
        document.getElementById('preview-delete-footer-btn').addEventListener('click', deleteCurrentPreview);

        modal.addEventListener('click', (e) => {
            if (e.target === modal || e.target.classList.contains('photo-preview-body')) {
                closePreviewModal();
            }
        });

        document.addEventListener('keydown', (e) => {
            if (!previewModalOpen) return;
            if (e.key === 'Escape') closePreviewModal();
            if (e.key === 'ArrowLeft') navigatePreview(-1);
            if (e.key === 'ArrowRight') navigatePreview(1);
            if (e.key === 'Delete' || e.key === 'Backspace') {
                e.preventDefault();
                deleteCurrentPreview();
            }
        });
    }

    function openPreviewModal(index) {
        if (uploadQueue.length === 0) return;

        currentPreviewIndex = Math.max(0, Math.min(index, uploadQueue.length - 1));
        const modal = document.getElementById('photo-preview-modal');
        const item = uploadQueue[currentPreviewIndex];

        modal.querySelector('.file-name').textContent = item.file.name;
        modal.querySelector('.photo-preview-body img').src = item.previewUrl;
        modal.querySelector('.photo-preview-counter').textContent = `${currentPreviewIndex + 1} of ${uploadQueue.length}`;

        const dims = modal.querySelector('.preview-dimensions');
        const size = modal.querySelector('.preview-size');
        if (item.width && item.height) {
            dims.textContent = `${item.width} × ${item.height}`;
        } else {
            dims.textContent = '';
        }
        size.textContent = formatFileSize(item.file.size);

        const prevBtn = modal.querySelector('#preview-prev-btn');
        const nextBtn = modal.querySelector('#preview-next-btn');
        prevBtn.disabled = currentPreviewIndex === 0;
        nextBtn.disabled = currentPreviewIndex >= uploadQueue.length - 1;
        prevBtn.style.visibility = currentPreviewIndex > 0 ? 'visible' : 'hidden';
        nextBtn.style.visibility = currentPreviewIndex < uploadQueue.length - 1 ? 'visible' : 'hidden';

        modal.classList.add('active');
        previewModalOpen = true;
        document.body.style.overflow = 'hidden';
    }

    function closePreviewModal() {
        const modal = document.getElementById('photo-preview-modal');
        modal.classList.remove('active');
        previewModalOpen = false;
        document.body.style.overflow = '';
    }

    function navigatePreview(direction) {
        const newIndex = currentPreviewIndex + direction;
        if (newIndex >= 0 && newIndex < uploadQueue.length) {
            openPreviewModal(newIndex);
        }
    }

    function deleteCurrentPreview() {
        if (uploadQueue.length === 0) return;

        const item = uploadQueue[currentPreviewIndex];
        if (item.status !== 'pending') return;

        removeFromUpload(item.id);

        if (uploadQueue.length === 0) {
            closePreviewModal();
        } else {
            currentPreviewIndex = Math.min(currentPreviewIndex, uploadQueue.length - 1);
            openPreviewModal(currentPreviewIndex);
        }
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
                previewUrl: null,
                width: 0,
                height: 0
            };

            uploadQueue.push(item);

            const reader = new FileReader();
            reader.onload = (e) => {
                item.previewUrl = e.target.result;

                const img = new Image();
                img.onload = () => {
                    item.width = img.width;
                    item.height = img.height;
                    addPreviewItem(item);
                };
                img.onerror = () => {
                    addPreviewItem(item);
                };
                img.src = e.target.result;
            };
            reader.readAsDataURL(file);
        });

        updateUI();
    }

    function addPreviewItem(item) {
        const aspectRatio = item.width && item.height ? (item.width / item.height) : 1;

        const div = document.createElement('div');
        div.className = 'upload-preview-item';
        div.id = `preview-${item.id}`;
        div.dataset.uploadId = item.id;

        const containerHeight = aspectRatio > 1 ? 120 : Math.min(180, 120 / aspectRatio);

        div.innerHTML = `
            <div class="preview-image-wrapper" style="height: ${containerHeight}px;">
                <div class="preview-blurhash"></div>
                <img src="${item.previewUrl}" alt="${escapeHtml(item.file.name)}" loading="lazy">
            </div>
            <button class="remove-btn" type="button" title="Remove">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                </svg>
            </button>
            <div class="file-info">
                <span class="file-name-text">${escapeHtml(item.file.name)}</span>
                <span class="file-meta">${item.width}×${item.height} • ${formatFileSize(item.file.size)}</span>
            </div>
            <div class="progress-overlay">
                <div class="progress-circle"></div>
                <div class="progress-text">0%</div>
            </div>
        `;
        previewGrid.appendChild(div);

        const img = div.querySelector('img');
        img.onload = () => {
            div.classList.add('image-loaded');
        };
    }

    function removeFromUpload(id) {
        const idx = uploadQueue.findIndex(item => item.id === id);
        if (idx === -1) return;

        if (uploadQueue[idx].status !== 'pending') return;

        uploadQueue.splice(idx, 1);
        const element = document.getElementById(`preview-${id}`);
        if (element) {
            element.classList.add('removing');
            setTimeout(() => element.remove(), 200);
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

    function formatFileSize(bytes) {
        if (bytes < 1024) return bytes + ' B';
        if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
        return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
    }

    updateUI();
})();