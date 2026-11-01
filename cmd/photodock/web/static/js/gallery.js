(function() {
    const GAP = 4;
    const MIN_WIDTH = 100;
    const MAX_IMAGE_REQUESTS = 4;
    let imageObserver = null;
    let activeImages = 0;
    const imageQueue = [];

    function targetRowHeight() {
        const w = window.innerWidth;
        if (w <= 480) return 130;
        if (w <= 768) return 170;
        if (w <= 1100) return 210;
        return 260;
    }

    function inlineAspect(item) {
        const el = item.querySelector('.progressive-image');
        if (el && el.style.aspectRatio) {
            const parts = el.style.aspectRatio.split('/');
            const w = parseFloat(parts[0]);
            const h = parseFloat(parts[1]);
            if (w > 0 && h > 0) return w / h;
        }
        return 0;
    }

    function itemAspect(item) {
        if (item._ar > 0) return item._ar;
        const ar = inlineAspect(item);
        item._ar = ar > 0 ? ar : 4 / 3;
        return item._ar;
    }

    function orderedGridItems(grid, orderedItems) {
        if (orderedItems) return orderedItems;
        const items = Array.from(grid.querySelectorAll('.photo-item'));
        if (items.length && items[0].dataset.idx !== undefined) {
            items.sort((a, b) => (parseInt(a.dataset.idx) || 0) - (parseInt(b.dataset.idx) || 0));
        }
        return items;
    }

    function layoutJustified(orderedItems) {
        const grid = document.getElementById('gallery');
        if (!grid) return;

        const items = orderedGridItems(grid, orderedItems);
        if (!items.length) return;
        items.forEach((item, i) => { item.dataset.idx = i; });

        const trigger = grid.querySelector('.load-more-trigger');
        const containerWidth = grid.clientWidth;
        if (containerWidth < MIN_WIDTH) return;
        const target = targetRowHeight();

        const rows = [];
        let row = [];
        let aspectSum = 0;

        items.forEach(item => {
            const ar = itemAspect(item);
            row.push({ item, ar });
            aspectSum += ar;
            const h = (containerWidth - GAP * (row.length - 1)) / aspectSum;
            if (h <= target) {
                rows.push({ row, height: h, full: true });
                row = [];
                aspectSum = 0;
            }
        });
        if (row.length) {
            const h = (containerWidth - GAP * (row.length - 1)) / aspectSum;
            rows.push({ row, height: Math.min(h, target), full: false });
        }

        const frag = document.createDocumentFragment();
        rows.forEach(r => {
            const div = document.createElement('div');
            div.className = 'justified-row';
            const rowHeight = Math.round(r.height);
            const lastIdx = r.row.length - 1;
            let used = 0;

            r.row.forEach(({ item, ar }, idx) => {
                let w;
                if (r.full && idx === lastIdx) {
                    w = containerWidth - GAP * lastIdx - used;
                } else {
                    w = Math.round(r.height * ar);
                    used += w;
                }
                item.style.width = w + 'px';
                item.style.height = rowHeight + 'px';
                div.appendChild(item);
            });
            frag.appendChild(div);
        });

        grid.querySelectorAll('.justified-row').forEach(r => r.remove());
        grid.appendChild(frag);
        if (trigger) grid.appendChild(trigger);
        updateVisibleImages();
    }

    function observeGrid() {
        const grid = document.getElementById('gallery');
        if (!grid || grid.dataset.justifiedObserved) return;
        grid.dataset.justifiedObserved = 'true';

        let lastWidth = grid.clientWidth;

        if ('ResizeObserver' in window) {
            const ro = new ResizeObserver(entries => {
                for (const entry of entries) {
                    const w = entry.target.clientWidth;
                    if (Math.abs(w - lastWidth) > 1) {
                        lastWidth = w;
                        layoutJustified();
                    }
                }
            });
            ro.observe(grid);
        } else {
            let resizeTimer = null;
            window.addEventListener('resize', () => {
                clearTimeout(resizeTimer);
                resizeTimer = setTimeout(() => {
                    const w = grid.clientWidth;
                    if (Math.abs(w - lastWidth) > 1) {
                        lastWidth = w;
                        layoutJustified();
                    }
                }, 120);
            });
        }
    }

    // Keep the placeholder and image in the same fixed box; loading must never rebuild the rows.
    function loadImage(container) {
        const current = container.querySelector('.full-image');
        if (!current || !container.dataset.small || container.clientWidth <= 0) return;
        const size = PhotoImages.choose(container.clientWidth, Number(container.dataset.width));
        if (size.width <= (container._loadedWidth || 0) || size.width <= (container._requestedWidth || 0)) return;

        container._requestedWidth = size.width;
        imageQueue.push({ container, size });
        drainImageQueue();
    }

    // Avoid starting dozens of server-side resizes for photos scrolled past or left behind.
    function drainImageQueue() {
        while (activeImages < MAX_IMAGE_REQUESTS && imageQueue.length) {
            const { container, size } = imageQueue.shift();
            if (container.dataset.inView !== 'true' || container._requestedWidth !== size.width) {
                if (container._requestedWidth === size.width) container._requestedWidth = 0;
                continue;
            }
            activeImages++;
            requestImage(container, size);
        }
    }

    function requestImage(container, size) {
        const current = container.querySelector('.full-image');
        const image = new Image();
        image.className = 'full-image';
        image.alt = current.alt;
        image.decoding = 'async';
        image.src = PhotoImages.url(container.dataset.small, size.name);
        PhotoImages.ready(image).then(() => {
            if (size.width <= (container._loadedWidth || 0)) return;
            container._loadedWidth = size.width;
            if (container._requestedWidth === size.width) container._requestedWidth = 0;
            const old = container.querySelector('.full-image');
            if (old) old.replaceWith(image);
            if (!container.classList.contains('loaded')) {
                requestAnimationFrame(() => requestAnimationFrame(() => container.classList.add('loaded')));
            }
        }).catch(() => {
            if (container._requestedWidth === size.width) container._requestedWidth = 0;
        }).finally(() => {
            activeImages--;
            drainImageQueue();
        });
    }

    function updateVisibleImages() {
        document.querySelectorAll('.progressive-image[data-in-view="true"]').forEach(loadImage);
    }

    function initLazyLoading() {
        if ('IntersectionObserver' in window && !imageObserver) {
            imageObserver = new IntersectionObserver(entries => {
                entries.forEach(entry => {
                    entry.target.dataset.inView = String(entry.isIntersecting);
                    if (entry.isIntersecting) loadImage(entry.target);
                });
            }, { rootMargin: '300px 0px' });
        }
        document.querySelectorAll('.progressive-image:not([data-observed-image])').forEach(container => {
            container.dataset.observedImage = 'true';
            if (imageObserver) imageObserver.observe(container);
            else {
                container.dataset.inView = 'true';
                loadImage(container);
            }
        });
    }

    function initFolderLazyLoading() {
        const folderImages = document.querySelectorAll('.folder-cover img.lazy:not([data-observed])');

        if ('IntersectionObserver' in window) {
            const folderObserver = new IntersectionObserver((entries, obs) => {
                entries.forEach(entry => {
                    if (entry.isIntersecting) {
                        const img = entry.target;
                        const src = img.dataset.src;
                        if (src) {
                            img.onload = () => {
                                img.classList.remove('lazy');
                                img.classList.add('loaded');
                            };
                            img.onerror = () => {
                                img.classList.remove('lazy');
                                img.classList.add('loaded');
                            };
                            img.src = src;
                        }
                        obs.unobserve(img);
                    }
                });
            }, { rootMargin: '100px 0px', threshold: 0 });

            folderImages.forEach(img => {
                img.dataset.observed = 'true';
                folderObserver.observe(img);
            });
        } else {
            folderImages.forEach(img => {
                const src = img.dataset.src;
                if (src) {
                    img.src = src;
                    img.classList.remove('lazy');
                    img.classList.add('loaded');
                }
            });
        }
    }

    function revealGrid() {
        const grid = document.getElementById('gallery');
        if (grid) grid.classList.add('jl-ready');
    }

    function init() {
        layoutJustified();
        // Reveal now that items are in their final positions (see the html.js #gallery CSS rule).
        revealGrid();
        observeGrid();
        initLazyLoading();
        initFolderLazyLoading();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    window.layoutJustified = layoutJustified;
    window.initGalleryLazyLoading = initLazyLoading;
    window.initFolderLazyLoading = initFolderLazyLoading;
})();
