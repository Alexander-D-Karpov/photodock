(function() {
    const GAP = 4;
    const MIN_WIDTH = 100;
    const ASPECT_TOLERANCE = 0.02;

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
    }

    let relayoutTimer = null;
    function scheduleRelayout() {
        clearTimeout(relayoutTimer);
        relayoutTimer = setTimeout(() => layoutJustified(), 60);
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

    // Thumbnails are auto-oriented, so their natural size beats the DB aspect (wrong for portraits).
    function correctAspect(container, img) {
        if (!img.naturalWidth || !img.naturalHeight) return;
        const item = container.closest('.photo-item');
        if (!item) return;
        const natural = img.naturalWidth / img.naturalHeight;
        const current = itemAspect(item);
        if (Math.abs(natural - current) / current > ASPECT_TOLERANCE) {
            item._ar = natural;
            container.style.aspectRatio = img.naturalWidth + ' / ' + img.naturalHeight;
            scheduleRelayout();
        }
    }

    // Preload medium fully, then swap src so the cached higher-res image paints with no flash.
    function upgradeToMedium(container) {
        if (container.dataset.upgraded) return;
        const img = container.querySelector('.full-image');
        if (!img) return;
        const mediumSrc = img.dataset.medium;
        if (!mediumSrc || img.src.indexOf(mediumSrc) !== -1) {
            container.dataset.upgraded = 'true';
            return;
        }
        container.dataset.upgraded = 'pending';
        const hi = new Image();
        hi.onload = () => {
            img.src = mediumSrc;
            container.dataset.upgraded = 'true';
        };
        hi.onerror = () => { container.dataset.upgraded = ''; };
        hi.src = mediumSrc;
    }

    let mediumObserver = null;
    function initProgressiveUpgrade() {
        const containers = document.querySelectorAll('.progressive-image:not([data-observed-medium])');
        if (!('IntersectionObserver' in window)) {
            containers.forEach(c => {
                c.dataset.observedMedium = 'true';
                upgradeToMedium(c);
            });
            return;
        }
        if (!mediumObserver) {
            mediumObserver = new IntersectionObserver((entries, obs) => {
                entries.forEach(entry => {
                    if (entry.isIntersecting) {
                        upgradeToMedium(entry.target);
                        obs.unobserve(entry.target);
                    }
                });
            }, { rootMargin: '300px 0px' });
        }
        containers.forEach(c => {
            c.dataset.observedMedium = 'true';
            mediumObserver.observe(c);
        });
    }

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

            const onLoaded = () => {
                container.classList.add('loaded');
                correctAspect(container, fullImage);
            };

            if (fullImage.complete && fullImage.naturalHeight > 0) {
                onLoaded();
            } else {
                fullImage.addEventListener('load', onLoaded, { once: true });
                fullImage.addEventListener('error', () => container.classList.add('loaded'), { once: true });
            }
        });
        initProgressiveUpgrade();
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