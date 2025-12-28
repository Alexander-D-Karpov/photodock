(function() {
    const lazyImages = document.querySelectorAll('img.lazy');

    if ('loading' in HTMLImageElement.prototype) {
        lazyImages.forEach(img => {
            if (img.dataset.src) {
                img.loading = 'lazy';
                img.src = img.dataset.src;
                img.classList.remove('lazy');
            }
        });
    } else {
        const imageObserver = new IntersectionObserver((entries, observer) => {
            entries.forEach(entry => {
                if (entry.isIntersecting) {
                    const img = entry.target;
                    const placeholder = img.dataset.placeholder;

                    if (placeholder) {
                        img.src = placeholder;
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

        lazyImages.forEach(img => imageObserver.observe(img));
    }
})();