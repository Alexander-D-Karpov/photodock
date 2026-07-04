(function() {
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
        layoutMasonry();
        initLazyLoading();
        initFolderLazyLoading();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    function masonryColumnCount() {
        const w = window.innerWidth;
        if (w <= 768) return 2;
        if (w <= 1100) return 3;
        return 4;
    }

    function itemAspect(item) {
        const el = item.querySelector('.progressive-image');
        if (el && el.style.aspectRatio) {
            const parts = el.style.aspectRatio.split('/');
            const w = parseFloat(parts[0]);
            const h = parseFloat(parts[1]);
            if (w > 0 && h > 0) return w / h;
        }
        return 1;
    }

    function layoutMasonry(orderedItems) {
        const grid = document.getElementById('gallery');
        if (!grid) return;

        let items = orderedItems;
        if (!items) {
            items = Array.from(grid.querySelectorAll('.photo-item'));
            if (items.length && items[0].dataset.idx !== undefined) {
                items.sort((a, b) => (parseInt(a.dataset.idx) || 0) - (parseInt(b.dataset.idx) || 0));
            }
        }
        items.forEach((item, i) => { item.dataset.idx = i; });

        const trigger = grid.querySelector('.load-more-trigger');
        grid.querySelectorAll('.masonry-col').forEach(c => c.remove());

        const colCount = masonryColumnCount();
        const cols = [];
        const heights = [];
        for (let i = 0; i < colCount; i++) {
            const col = document.createElement('div');
            col.className = 'masonry-col';
            cols.push(col);
            heights.push(0);
        }

        items.forEach(item => {
            let min = 0;
            for (let i = 1; i < colCount; i++) {
                if (heights[i] < heights[min]) min = i;
            }
            cols[min].appendChild(item);
            heights[min] += 1 / itemAspect(item);
        });

        cols.forEach(c => grid.appendChild(c));
        if (trigger) grid.appendChild(trigger);
    }

    let resizeTimer = null;
    let lastColCount = masonryColumnCount();
    window.addEventListener('resize', () => {
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(() => {
            const c = masonryColumnCount();
            if (c !== lastColCount) {
                lastColCount = c;
                layoutMasonry();
            }
        }, 150);
    });

    window.layoutMasonry = layoutMasonry;
    window.initGalleryLazyLoading = initLazyLoading;
    window.initFolderLazyLoading = initFolderLazyLoading;
})();