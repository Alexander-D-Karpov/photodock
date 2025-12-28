(function() {
    const viewBtns = document.querySelectorAll('.view-btn');
    const fileList = document.getElementById('file-list');
    const gridView = document.getElementById('grid-view');

    const savedView = localStorage.getItem('photodock-view') || 'grid';
    setView(savedView);

    viewBtns.forEach(btn => {
        btn.addEventListener('click', () => {
            const view = btn.dataset.view;
            setView(view);
            localStorage.setItem('photodock-view', view);
        });
    });

    function setView(view) {
        viewBtns.forEach(b => b.classList.toggle('active', b.dataset.view === view));
        if (view === 'list') {
            if (fileList) fileList.style.display = '';
            if (gridView) gridView.style.display = 'none';
        } else {
            if (fileList) fileList.style.display = 'none';
            if (gridView) gridView.style.display = '';
            initGallery();
        }
    }

    function initGallery() {
        const lazyImages = document.querySelectorAll('#grid-view img.lazy');
        const imageObserver = new IntersectionObserver((entries, observer) => {
            entries.forEach(entry => {
                if (entry.isIntersecting) {
                    const img = entry.target;
                    if (img.dataset.placeholder) {
                        img.src = img.dataset.placeholder;
                        img.classList.add('loading');
                    }
                    const fullImg = new Image();
                    fullImg.onload = () => {
                        img.src = img.dataset.src;
                        img.classList.remove('loading', 'lazy');
                    };
                    fullImg.src = img.dataset.src;
                    observer.unobserve(img);
                }
            });
        }, { rootMargin: '200px' });
        lazyImages.forEach(img => {
            if (!img.dataset.observed) {
                img.dataset.observed = 'true';
                imageObserver.observe(img);
            }
        });
    }

    const sortSelect = document.getElementById('sort-select');
    if (sortSelect) {
        sortSelect.addEventListener('change', () => {
            const [col, dir] = sortSelect.value.split('-');
            sortTable(col, dir);
            sortGrid(col, dir);
            localStorage.setItem('photodock-sort', sortSelect.value);
        });

        const savedSort = localStorage.getItem('photodock-sort');
        if (savedSort) {
            sortSelect.value = savedSort;
            const [col, dir] = savedSort.split('-');
            sortTable(col, dir);
            sortGrid(col, dir);
        }
    }

    function sortTable(col, dir) {
        if (!fileList) return;
        const tbody = fileList.querySelector('tbody');
        if (!tbody) return;

        const rows = Array.from(tbody.querySelectorAll('tr:not(.parent-row)'));
        const parentRow = tbody.querySelector('.parent-row');

        rows.sort((a, b) => {
            const aIsFolder = a.classList.contains('folder-row');
            const bIsFolder = b.classList.contains('folder-row');

            if (aIsFolder && !bIsFolder) return -1;
            if (!aIsFolder && bIsFolder) return 1;

            let aVal, bVal;

            switch (col) {
                case 'name':
                    aVal = (a.dataset.name || '').toLowerCase();
                    bVal = (b.dataset.name || '').toLowerCase();
                    break;
                case 'size':
                    aVal = parseInt(a.dataset.size) || 0;
                    bVal = parseInt(b.dataset.size) || 0;
                    break;
                case 'date':
                    aVal = parseInt(a.dataset.date) || 0;
                    bVal = parseInt(b.dataset.date) || 0;
                    break;
                default:
                    return 0;
            }

            if (typeof aVal === 'string') {
                if (aVal < bVal) return dir === 'asc' ? -1 : 1;
                if (aVal > bVal) return dir === 'asc' ? 1 : -1;
            } else {
                if (dir === 'asc') return aVal - bVal;
                return bVal - aVal;
            }
            return 0;
        });

        rows.forEach(row => tbody.appendChild(row));
        if (parentRow) tbody.insertBefore(parentRow, tbody.firstChild);
    }

    function sortGrid(col, dir) {
        if (!gridView) return;

        const folderGrid = gridView.querySelector('.folders-grid');
        const photoGrid = gridView.querySelector('.masonry');

        if (folderGrid) {
            const folders = Array.from(folderGrid.querySelectorAll('.folder-card'));
            folders.sort((a, b) => {
                let aVal, bVal;
                switch (col) {
                    case 'name':
                        aVal = (a.dataset.name || '').toLowerCase();
                        bVal = (b.dataset.name || '').toLowerCase();
                        break;
                    case 'date':
                        aVal = parseInt(a.dataset.date) || 0;
                        bVal = parseInt(b.dataset.date) || 0;
                        break;
                    default:
                        return 0;
                }
                if (typeof aVal === 'string') {
                    if (aVal < bVal) return dir === 'asc' ? -1 : 1;
                    if (aVal > bVal) return dir === 'asc' ? 1 : -1;
                } else {
                    if (dir === 'asc') return aVal - bVal;
                    return bVal - aVal;
                }
                return 0;
            });
            folders.forEach(f => folderGrid.appendChild(f));
        }

        if (photoGrid) {
            const photos = Array.from(photoGrid.querySelectorAll('.photo-item'));
            photos.sort((a, b) => {
                let aVal, bVal;
                switch (col) {
                    case 'name':
                        aVal = (a.dataset.name || '').toLowerCase();
                        bVal = (b.dataset.name || '').toLowerCase();
                        break;
                    case 'size':
                        aVal = parseInt(a.dataset.size) || 0;
                        bVal = parseInt(b.dataset.size) || 0;
                        break;
                    case 'date':
                        aVal = parseInt(a.dataset.date) || 0;
                        bVal = parseInt(b.dataset.date) || 0;
                        break;
                    default:
                        return 0;
                }
                if (typeof aVal === 'string') {
                    if (aVal < bVal) return dir === 'asc' ? -1 : 1;
                    if (aVal > bVal) return dir === 'asc' ? 1 : -1;
                } else {
                    if (dir === 'asc') return aVal - bVal;
                    return bVal - aVal;
                }
                return 0;
            });
            photos.forEach(p => photoGrid.appendChild(p));
        }
    }

    if (savedView === 'grid') {
        initGallery();
    }
})();