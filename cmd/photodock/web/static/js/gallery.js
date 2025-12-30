(function() {
    let currentPage = 1;
    let isLoading = false;
    let hasMore = true;
    let observer = null;

    function initLazyLoading() {
        const lazyImages = document.querySelectorAll('img.lazy:not([data-observed])');

        if ('IntersectionObserver' in window) {
            const imageObserver = new IntersectionObserver((entries, obs) => {
                entries.forEach(entry => {
                    if (entry.isIntersecting) {
                        loadImage(entry.target);
                        obs.unobserve(entry.target);
                    }
                });
            }, { rootMargin: '200px 0px', threshold: 0.01 });

            lazyImages.forEach(img => {
                img.dataset.observed = 'true';
                imageObserver.observe(img);
            });
        } else {
            lazyImages.forEach(img => loadImage(img));
        }
    }

    function loadImage(img) {
        const src = img.dataset.src;
        if (!src) return;

        const placeholder = img.dataset.placeholder;
        if (placeholder) {
            img.src = placeholder;
            img.classList.add('loading');
        }

        const fullImg = new Image();
        fullImg.onload = () => {
            img.src = src;
            img.classList.remove('loading', 'lazy');
            img.classList.add('loaded');
        };
        fullImg.onerror = () => {
            img.classList.remove('loading', 'lazy');
            img.classList.add('loaded');
        };
        fullImg.src = src;
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
            }, { rootMargin: '100px 0px', threshold: 0.01 });

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
        }, { rootMargin: '200px' });

        observer.observe(trigger);
    }

    async function loadMorePhotos() {
        if (isLoading || !hasMore) return;

        isLoading = true;
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

            const img = document.createElement('img');
            img.className = 'lazy';
            img.dataset.src = `/thumb/small/${photo.id}`;
            if (photo.blurhash) {
                img.dataset.placeholder = `/placeholder/${photo.id}`;
            }
            img.alt = photo.title || photo.filename;
            img.loading = 'lazy';

            a.appendChild(img);
            gallery.appendChild(a);
        });

        initLazyLoading();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    function init() {
        initLazyLoading();
        initFolderLazyLoading();
        initInfiniteScroll();
    }

    window.initGalleryLazyLoading = initLazyLoading;
    window.initFolderLazyLoading = initFolderLazyLoading;
})();