(function() {
    const GAP = 4;

    function targetRowHeight() {
        const w = window.innerWidth;
        if (w <= 480) return 130;
        if (w <= 768) return 170;
        if (w <= 1100) return 210;
        return 260;
    }

    function itemAspect(item) {
        const el = item.querySelector('.progressive-image');
        if (el && el.style.aspectRatio) {
            const parts = el.style.aspectRatio.split('/');
            const w = parseFloat(parts[0]);
            const h = parseFloat(parts[1]);
            if (w > 0 && h > 0) return w / h;
        }
        return 4 / 3;
    }

    function layoutJustified(orderedItems) {
        const grid = document.getElementById('gallery');
        if (!grid) return;

        let items = orderedItems;
        if (!items) {
            items = Array.from(grid.querySelectorAll('.photo-item'));
            if (items.length && items[0].dataset.idx !== undefined) {
                items.sort((a, b) => (parseInt(a.dataset.idx) || 0) - (parseInt(b.dataset.idx) || 0));
            }
        }
        if (!items.length) return;
        items.forEach((item, i) => { item.dataset.idx = i; });

        const trigger = grid.querySelector('.load-more-trigger');
        const containerWidth = grid.clientWidth;
        if (containerWidth <= 0) return;
        const target = targetRowHeight();

        let row = [];
        let aspectSum = 0;
        const rows = [];

        items.forEach(item => {
            const ar = itemAspect(item);
            row.push({ item, ar });
            aspectSum += ar;
            const h = (containerWidth - GAP * (row.length - 1)) / aspectSum;
            if (h <= target) {
                rows.push({ row, height: h });
                row = [];
                aspectSum = 0;
            }
        });
        if (row.length) {
            const h = (containerWidth - GAP * (row.length - 1)) / aspectSum;
            rows.push({ row, height: Math.min(h, target) });
        }

        grid.querySelectorAll('.justified-row').forEach(r => r.remove());

        rows.forEach(r => {
            const div = document.createElement('div');
            div.className = 'justified-row';
            r.row.forEach(({ item, ar }) => {
                item.style.width = Math.floor(r.height * ar) + 'px';
                item.style.height = Math.round(r.height) + 'px';
                div.appendChild(item);
            });
            grid.appendChild(div);
        });
        if (trigger) grid.appendChild(trigger);
    }

    let resizeTimer = null;
    let lastWidth = window.innerWidth;
    window.addEventListener('resize', () => {
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(() => {
            if (window.innerWidth !== lastWidth) {
                lastWidth = window.innerWidth;
                layoutJustified();
            }
        }, 120);
    });

    function initLazyLoading() {
        document.querySelectorAll('.progressive-image').forEach(container => {
            const fullImage = container.querySelector('.full-image');
            const placeholder = container.querySelector('.placeholder');
            if (!fullImage) return;

            if (placeholder) {
                if (placeholder.complete && placeholder.naturalHeight > 0) {
                    placeholder.classList.add('ready');
                } else {
                    placeholder.addEventListener('load', function() {
                        this.classList.add('ready');
                    }, { once: true });
                }
            }

            if (fullImage.complete && fullImage.naturalHeight > 0) {
                container.classList.add('loaded');
            } else {
                fullImage.addEventListener('load', function() {
                    container.classList.add('loaded');
                }, { once: true });

                fullImage.addEventListener('error', function() {
                    container.classList.add('loaded');
                }, { once: true });
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

    function init() {
        layoutJustified();
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