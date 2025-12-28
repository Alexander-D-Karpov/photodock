let selectedPhotos = new Set();

function scanAll() {
    if (!confirm('Scan all folders for new photos?')) return;
    fetch('/admin/scan', { method: 'POST' })
        .then(r => r.json())
        .then(() => alert('Scan started. Refresh page in a moment to see results.'));
}

function scanFolder(id) {
    fetch('/admin/scan/' + id, { method: 'POST' })
        .then(r => r.json())
        .then(() => alert('Folder scan started. Refresh to see results.'));
}

function cleanOrphans() {
    if (!confirm('Clean orphaned database entries?')) return;
    fetch('/admin/clean', { method: 'POST' })
        .then(r => r.json())
        .then(() => alert('Cleanup started. Refresh to see results.'));
}

function deleteFolder(id) {
    if (!confirm('Delete this folder? Photos will be moved to root.')) return;
    fetch('/admin/folders/' + id, { method: 'DELETE' })
        .then(r => {
            if (r.ok) location.reload();
            else alert('Failed to delete folder');
        });
}

function deletePhoto(id) {
    if (!confirm('Delete this photo permanently?')) return;
    fetch('/admin/photos/' + id, { method: 'DELETE' })
        .then(r => {
            if (r.ok) {
                const card = document.querySelector(`[data-id="${id}"]`);
                if (card) card.remove();
                selectedPhotos.delete(id);
                updateBulkUI();
            } else {
                alert('Failed to delete photo');
            }
        });
}

function toggleHide(id) {
    fetch('/admin/photos/' + id + '/hide', { method: 'POST' })
        .then(r => {
            if (r.ok) location.reload();
            else alert('Failed to toggle visibility');
        });
}

function setCover(folderId, photoId) {
    const body = new FormData();
    if (photoId !== null) body.append('photo_id', photoId);
    fetch('/admin/folders/' + folderId + '/cover', { method: 'POST', body })
        .then(r => {
            if (r.ok) location.reload();
            else alert('Failed to set cover');
        });
}

function showCreateFolder() {
    document.getElementById('create-folder-dialog').showModal();
}

function toggleTreeItem(id) {
    const item = document.querySelector(`.tree-item[data-id="${id}"]`);
    if (!item) return;

    const toggle = item.querySelector('.tree-toggle');
    const isCollapsed = item.classList.contains('collapsed');

    if (isCollapsed) {
        item.classList.remove('collapsed');
        toggle.classList.add('expanded');
        showChildren(id);
    } else {
        item.classList.add('collapsed');
        toggle.classList.remove('expanded');
        hideChildren(id);
    }
}

function hideChildren(parentId) {
    const children = document.querySelectorAll(`.tree-item[data-parent="${parentId}"]`);
    children.forEach(child => {
        child.classList.add('hiding');
        const childId = child.dataset.id;
        hideChildren(childId);
    });
}

function showChildren(parentId) {
    const children = document.querySelectorAll(`.tree-item[data-parent="${parentId}"]`);
    children.forEach(child => {
        child.classList.remove('hiding');
        if (!child.classList.contains('collapsed')) {
            const childId = child.dataset.id;
            showChildren(childId);
        }
    });
}

function togglePhotoSelect(id, checkbox) {
    if (checkbox.checked) {
        selectedPhotos.add(id);
    } else {
        selectedPhotos.delete(id);
    }
    updateBulkUI();
}

function toggleSelectAll(checkbox) {
    const allCheckboxes = document.querySelectorAll('.photo-select');
    allCheckboxes.forEach(cb => {
        const id = parseInt(cb.dataset.id);
        cb.checked = checkbox.checked;
        if (checkbox.checked) {
            selectedPhotos.add(id);
        } else {
            selectedPhotos.delete(id);
        }
    });
    updateBulkUI();
}

function updateBulkUI() {
    const bulkActions = document.getElementById('bulk-actions');
    const selectedCount = document.getElementById('selected-count');

    if (bulkActions) {
        bulkActions.style.display = selectedPhotos.size > 0 ? 'flex' : 'none';
    }
    if (selectedCount) {
        selectedCount.textContent = selectedPhotos.size;
    }
}

function bulkHide() {
    if (selectedPhotos.size === 0) return;
    if (!confirm(`Hide ${selectedPhotos.size} selected photos?`)) return;

    const promises = Array.from(selectedPhotos).map(id =>
        fetch('/admin/photos/' + id + '/hide', { method: 'POST' })
    );

    Promise.all(promises).then(() => location.reload());
}

function bulkDelete() {
    if (selectedPhotos.size === 0) return;
    if (!confirm(`Delete ${selectedPhotos.size} selected photos permanently?`)) return;

    const promises = Array.from(selectedPhotos).map(id =>
        fetch('/admin/photos/' + id, { method: 'DELETE' })
    );

    Promise.all(promises).then(() => location.reload());
}

function bulkMove() {
    if (selectedPhotos.size === 0) return;
    const dialog = document.getElementById('move-dialog');
    if (dialog) dialog.showModal();
}

function confirmBulkMove() {
    const folderId = document.getElementById('move-folder').value;
    const promises = Array.from(selectedPhotos).map(id => {
        const body = new FormData();
        body.append('folder_id', folderId);
        return fetch('/admin/photos/' + id + '/move', { method: 'POST', body });
    });

    Promise.all(promises).then(() => location.reload());
}

function performSearch() {
    const query = document.getElementById('search-input').value;
    const url = new URL(window.location);
    if (query) {
        url.searchParams.set('q', query);
    } else {
        url.searchParams.delete('q');
    }
    url.searchParams.delete('page');
    window.location = url;
}

document.addEventListener('DOMContentLoaded', () => {
    const folderSelect = document.getElementById('upload-folder');
    if (folderSelect && folderSelect.options.length <= 1) {
        fetch('/admin/folders')
            .then(r => r.text())
            .then(html => {
                const parser = new DOMParser();
                const doc = parser.parseFromString(html, 'text/html');
                const items = doc.querySelectorAll('.tree-item');
                items.forEach(item => {
                    const name = item.querySelector('.tree-name');
                    const path = item.querySelector('.tree-path');
                    if (name && path) {
                        const opt = document.createElement('option');
                        opt.value = item.dataset.id;
                        const depth = parseInt(item.dataset.depth) || 0;
                        opt.textContent = '\u00A0\u00A0'.repeat(depth) + name.textContent;
                        folderSelect.appendChild(opt);
                    }
                });
            })
            .catch(() => {});
    }

    document.querySelectorAll('.tree-toggle').forEach(toggle => {
        toggle.classList.add('expanded');
    });

    const searchInput = document.getElementById('search-input');
    if (searchInput) {
        searchInput.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') {
                performSearch();
            }
        });
    }
});