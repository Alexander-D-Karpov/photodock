(function() {
    let currentPage = 1;
    let isLoading = false;
    let hasMore = true;
    let observer = null;

    function initLazyLoading() {
        document.querySelectorAll('.progressive-image').forEach(container => {
            const fullImage = container.querySelector('.full-image');
            if (!fullImage) return;

            if (fullImage.complete && fullImage.naturalHeight > 0) {
                container.classList.add('loaded');
            } else {
                fullImage.addEventListener('load', function() {
                    container.classList.add('loaded');
                }, { once: true });

                fullImage.addEventListener('error', function() {
                    container.classList.add('loaded');
                    container.classList.add('load-error');
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

    function initMasonry() {
        const gallery = document.getElementById('gallery');
        if (!gallery || !gallery.classList.contains('masonry')) return;

        const items = gallery.querySelectorAll('.photo-item');
        const gap = 15;

        function layoutMasonry() {
            requestAnimationFrame(() => {
                items.forEach(item => {
                    const container = item.querySelector('.progressive-image');
                    if (container) {
                        const height = container.offsetHeight || container.getBoundingClientRect().height;
                        if (height > 0) {
                            item.style.gridRowEnd = `span ${Math.ceil(height + gap)}`;
                        }
                    }
                });
            });
        }

        layoutMasonry();

        setTimeout(layoutMasonry, 200);
        setTimeout(layoutMasonry, 500);

        const resizeObserver = new ResizeObserver(() => {
            layoutMasonry();
        });

        items.forEach(item => {
            const container = item.querySelector('.progressive-image');
            if (container) {
                resizeObserver.observe(container);
            }
        });

        const mutationObserver = new MutationObserver((mutations) => {
            mutations.forEach(mutation => {
                if (mutation.type === 'childList' && mutation.addedNodes.length > 0) {
                    setTimeout(layoutMasonry, 100);
                }
            });
        });

        mutationObserver.observe(gallery, { childList: true });
    }

    function initInfiniteScroll() {
        const gallery = document.getElementById('gallery');
        const trigger = document.getElementById('load-more-trigger');

        if (!gallery || !trigger) return;

        const totalPhotos = parseInt(gallery.dataset.total || '0');
        const loadedPhotos = gallery.querySelectorAll('.photo-item').length;
        hasMore = loadedPhotos < totalPhotos;

        if (!hasMore) {
            trigger.innerHTML = '<span class="load-more-end">That\'s all folks!</span>';
            return;
        }

        observer = new IntersectionObserver((entries) => {
            if (entries[0].isIntersecting && !isLoading && hasMore) {
                loadMorePhotos();
            }
        }, { rootMargin: '400px' });

        observer.observe(trigger);
    }

    async function loadMorePhotos() {
        if (isLoading || !hasMore) return;

        isLoading = true;
        const gallery = document.getElementById('gallery');
        const trigger = document.getElementById('load-more-trigger');

        if (trigger) {
            trigger.innerHTML = '<div class="load-more-spinner"></div>';
        }

        currentPage++;
        const url = new URL(window.location);
        url.searchParams.set('page', currentPage);
        url.searchParams.set('ajax', '1');

        try {
            const res = await fetch(url);
            const data = await res.json();

            if (data.photos && data.photos.length > 0) {
                appendPhotos(data.photos);
                hasMore = data.hasMore;
            } else {
                hasMore = false;
            }

            if (!hasMore && trigger) {
                trigger.innerHTML = '<span class="load-more-end">That\'s all folks!</span>';
                if (observer) observer.disconnect();
            } else if (trigger) {
                trigger.innerHTML = '';
            }
        } catch (err) {
            console.error('Failed to load more photos:', err);
            if (trigger) {
                trigger.innerHTML = '<span class="load-more-end">Failed to load</span>';
            }
        }

        isLoading = false;
    }

    function appendPhotos(photos) {
        const gallery = document.getElementById('gallery');
        if (!gallery) return;

        photos.forEach(photo => {
            const a = document.createElement('a');
            a.href = photo.url;
            a.className = 'photo-item';
            a.dataset.id = photo.id;
            a.dataset.name = photo.filename;
            a.dataset.size = photo.size;
            a.dataset.date = photo.date;

            const aspectRatio = photo.width && photo.height ? (photo.width / photo.height) : (4/3);
            const aspectStyle = photo.width && photo.height
                ? `aspect-ratio: ${photo.width}/${photo.height}`
                : 'aspect-ratio: 4/3; min-height: 200px';

            a.innerHTML = `
                <div class="progressive-image" style="${aspectStyle}">
                    <div class="skeleton-shimmer"></div>
                    ${photo.blurhash ? `<img class="placeholder" src="/placeholder/${photo.id}" alt="" aria-hidden="true">` : ''}
                    <img class="full-image" src="/thumb/small/${photo.id}" 
                         alt="${photo.title || photo.filename}" loading="lazy"
                         ${photo.width ? `width="${photo.width}"` : ''} ${photo.height ? `height="${photo.height}"` : ''}
                         onload="this.parentElement.classList.add('loaded')">
                </div>
            `;

            gallery.insertBefore(a, document.getElementById('load-more-trigger'));
        });

        initLazyLoading();
        initMasonry();
    }

    function init() {
        initLazyLoading();
        initFolderLazyLoading();
        initMasonry();
        initInfiniteScroll();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    window.initGalleryLazyLoading = initLazyLoading;
    window.initFolderLazyLoading = initFolderLazyLoading;
    window.initMasonry = initMasonry;
})();