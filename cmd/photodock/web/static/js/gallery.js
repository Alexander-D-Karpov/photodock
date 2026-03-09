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
        initLazyLoading();
        initFolderLazyLoading();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    window.initGalleryLazyLoading = initLazyLoading;
    window.initFolderLazyLoading = initFolderLazyLoading;
})();