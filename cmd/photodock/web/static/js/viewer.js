const CLICK_ZOOM = 1.8;
const MIN_ZOOM = 1;
const MAX_ZOOM = 3;
const DRAG_THRESHOLD = 4;

let viewerState = {
    zoomed: false,
    scale: 1,
    translateX: 0,
    translateY: 0,

    startX: 0,
    startY: 0,
    isDragging: false,

    dragStartClientX: 0,
    dragStartClientY: 0,
    dragMoved: false,
    suppressNextClick: false,

    lastTouchDist: 0
};

function initViewer(opts) {
    const sidebar = document.getElementById('sidebar');
    const toggle = document.getElementById('sidebar-toggle');
    const img = document.getElementById('main-image');
    const container = document.querySelector('.viewer-image');

    if (toggle && sidebar) {
        toggle.addEventListener('click', () => {
            sidebar.classList.toggle('open');
        });
    }

    // Preload adjacent originals (best-effort)
    if (opts.prevId) {
        const prevImg = new Image();
        prevImg.src = '/original/' + opts.prevId;
    }
    if (opts.nextId) {
        const nextImg = new Image();
        nextImg.src = '/original/' + opts.nextId;
    }

    document.addEventListener('keydown', (e) => {
        if (e.target && (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA')) return;

        if (e.key === 'ArrowLeft' && opts.prevUrl) {
            window.location.href = opts.prevUrl;
        } else if (e.key === 'ArrowRight' && opts.nextUrl) {
            window.location.href = opts.nextUrl;
        } else if (e.key === 'Escape') {
            if (img && viewerState.zoomed) {
                resetZoom(img);
            } else {
                goBack();
            }
        } else if (e.key === 'i' && sidebar) {
            sidebar.classList.toggle('open');
        }
    });

    if (!img || !container) {
        window.viewerOpts = opts;
        return;
    }

    // Click empty (black) area to unzoom (desktop fix)
    container.addEventListener('click', (e) => {
        if (!viewerState.zoomed) return;
        if (e.target !== container) return; // only when clicking outside the image
        resetZoom(img);
    });

    // Click image to zoom/unzoom
    img.addEventListener('click', (e) => {
        e.stopPropagation();

        if (viewerState.suppressNextClick) {
            viewerState.suppressNextClick = false;
            return;
        }

        if (viewerState.zoomed) {
            resetZoom(img);
        } else {
            zoomToPoint(img, container, e.clientX, e.clientY);
        }
    });

    img.addEventListener('mousedown', (e) => {
        if (!viewerState.zoomed) return;
        e.preventDefault();

        viewerState.isDragging = true;
        viewerState.dragMoved = false;

        viewerState.dragStartClientX = e.clientX;
        viewerState.dragStartClientY = e.clientY;

        viewerState.startX = e.clientX - viewerState.translateX;
        viewerState.startY = e.clientY - viewerState.translateY;

        img.style.cursor = 'grabbing';
    });

    document.addEventListener('mousemove', (e) => {
        if (!viewerState.isDragging) return;

        const dx = e.clientX - viewerState.dragStartClientX;
        const dy = e.clientY - viewerState.dragStartClientY;
        if (!viewerState.dragMoved && (Math.abs(dx) > DRAG_THRESHOLD || Math.abs(dy) > DRAG_THRESHOLD)) {
            viewerState.dragMoved = true;
        }

        viewerState.translateX = e.clientX - viewerState.startX;
        viewerState.translateY = e.clientY - viewerState.startY;
        updateTransform(img);
    });

    document.addEventListener('mouseup', () => {
        if (viewerState.isDragging) {
            viewerState.isDragging = false;

            if (viewerState.dragMoved) {
                viewerState.suppressNextClick = true;
            }
        }
        img.style.cursor = viewerState.zoomed ? 'grab' : 'zoom-in';
    });

    // Touch: pinch to zoom + pan
    container.addEventListener('touchstart', (e) => {
        if (e.touches.length === 2) {
            e.preventDefault();
            viewerState.lastTouchDist = getTouchDistance(e.touches);
        } else if (e.touches.length === 1 && viewerState.zoomed) {
            viewerState.isDragging = true;
            viewerState.dragMoved = false;

            viewerState.dragStartClientX = e.touches[0].clientX;
            viewerState.dragStartClientY = e.touches[0].clientY;

            viewerState.startX = e.touches[0].clientX - viewerState.translateX;
            viewerState.startY = e.touches[0].clientY - viewerState.translateY;
        }
    }, { passive: false });

    container.addEventListener('touchmove', (e) => {
        if (e.touches.length === 2) {
            e.preventDefault();

            const dist = getTouchDistance(e.touches);
            const delta = dist / (viewerState.lastTouchDist || dist);
            viewerState.lastTouchDist = dist;

            const nextScale = clamp(viewerState.scale * delta, MIN_ZOOM, MAX_ZOOM);
            viewerState.scale = nextScale;

            if (viewerState.scale > 1.001) {
                viewerState.zoomed = true;
                img.classList.add('zoomed');
                updateNavVisibility(true);
            } else {
                viewerState.scale = 1;
            }

            updateTransform(img);
        } else if (e.touches.length === 1 && viewerState.isDragging) {
            e.preventDefault();

            const dx = e.touches[0].clientX - viewerState.dragStartClientX;
            const dy = e.touches[0].clientY - viewerState.dragStartClientY;
            if (!viewerState.dragMoved && (Math.abs(dx) > DRAG_THRESHOLD || Math.abs(dy) > DRAG_THRESHOLD)) {
                viewerState.dragMoved = true;
            }

            viewerState.translateX = e.touches[0].clientX - viewerState.startX;
            viewerState.translateY = e.touches[0].clientY - viewerState.startY;
            updateTransform(img);
        }
    }, { passive: false });

    container.addEventListener('touchend', () => {
        viewerState.isDragging = false;

        // If zoom got back to ~1, fully reset.
        if (viewerState.scale <= 1.001) {
            resetZoom(img);
        }
    });

    // Swipe navigation (only when not zoomed)
    let touchStartX = 0;
    let touchStartTime = 0;

    container.addEventListener('touchstart', (e) => {
        if (e.touches.length === 1 && !viewerState.zoomed) {
            touchStartX = e.touches[0].clientX;
            touchStartTime = Date.now();
        }
    }, { passive: true });

    container.addEventListener('touchend', (e) => {
        if (viewerState.zoomed) return;
        if (!e.changedTouches || !e.changedTouches[0]) return;

        const touchEndX = e.changedTouches[0].clientX;
        const diff = touchStartX - touchEndX;
        const elapsed = Date.now() - touchStartTime;

        if (Math.abs(diff) > 50 && elapsed < 300) {
            if (diff > 0 && opts.nextUrl) {
                window.location.href = opts.nextUrl;
            } else if (diff < 0 && opts.prevUrl) {
                window.location.href = opts.prevUrl;
            }
        }
    }, { passive: true });

    window.viewerOpts = opts;
}

function zoomToPoint(img, container, clientX, clientY) {
    const rect = img.getBoundingClientRect();
    const containerRect = container.getBoundingClientRect();

    const imgX = (clientX - rect.left) / rect.width;
    const imgY = (clientY - rect.top) / rect.height;

    viewerState.scale = CLICK_ZOOM;
    viewerState.zoomed = true;

    const scaledWidth = rect.width * viewerState.scale;
    const scaledHeight = rect.height * viewerState.scale;

    viewerState.translateX = (containerRect.width / 2) - (imgX * scaledWidth);
    viewerState.translateY = (containerRect.height / 2) - (imgY * scaledHeight);

    img.classList.add('zoomed');
    updateTransform(img);
    updateNavVisibility(true);
}

function resetZoom(img) {
    viewerState.zoomed = false;
    viewerState.scale = 1;
    viewerState.translateX = 0;
    viewerState.translateY = 0;

    viewerState.isDragging = false;
    viewerState.dragMoved = false;
    viewerState.suppressNextClick = false;

    img.classList.remove('zoomed');
    img.style.transform = '';
    img.style.cursor = 'zoom-in';
    updateNavVisibility(false);
}

function updateTransform(img) {
    img.style.transform = `translate(${viewerState.translateX}px, ${viewerState.translateY}px) scale(${viewerState.scale})`;
    img.style.cursor = viewerState.zoomed ? 'grab' : 'zoom-in';
}

function updateNavVisibility(hidden) {
    const nav = document.querySelector('.viewer-nav');
    if (!nav) return;
    nav.style.pointerEvents = hidden ? 'none' : '';
    nav.style.opacity = hidden ? '0' : '';
}

function getTouchDistance(touches) {
    const dx = touches[0].clientX - touches[1].clientX;
    const dy = touches[0].clientY - touches[1].clientY;
    return Math.sqrt(dx * dx + dy * dy);
}

function clamp(v, min, max) {
    return Math.min(Math.max(v, min), max);
}

function goBack() {
    if (window.viewerOpts && window.viewerOpts.folderUrl) {
        window.location.href = window.viewerOpts.folderUrl;
    } else {
        window.location.href = '/';
    }
}

function toggleZoom(img) {
}
