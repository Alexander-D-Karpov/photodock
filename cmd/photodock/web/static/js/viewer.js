// cmd/photodock/web/static/js/viewer.js

let state = {
    scale: 1,
    x: 0,
    y: 0,
    dragging: false,
    dragStartX: 0,
    dragStartY: 0,
    startX: 0,
    startY: 0,
    moved: false
};

function initViewer(opts) {
    opts = opts || {};
    window.viewerOpts = opts;

    const sidebar = document.getElementById('sidebar');
    const toggle = document.getElementById('sidebar-toggle');
    const img = document.getElementById('main-image');
    const container = document.querySelector('.viewer-image');

    // Sidebar toggle
    if (toggle && sidebar) {
        toggle.addEventListener('click', () => sidebar.classList.toggle('open'));
    }

    // Prefetch neighbors (helps navigation feel instant)
    if (opts.prevId) new Image().src = '/original/' + opts.prevId;
    if (opts.nextId) new Image().src = '/original/' + opts.nextId;

    // Keyboard navigation
    document.addEventListener('keydown', (e) => {
        const tag = e.target && e.target.tagName;
        if (tag === 'INPUT' || tag === 'TEXTAREA') return;

        if (e.key === 'ArrowLeft' && opts.prevUrl) {
            window.location.href = opts.prevUrl;
        } else if (e.key === 'ArrowRight' && opts.nextUrl) {
            window.location.href = opts.nextUrl;
        } else if (e.key === 'Escape') {
            if (state.scale > 1.01) reset();
            else goBack();
        } else if ((e.key === 'i' || e.key === 'I') && sidebar) {
            sidebar.classList.toggle('open');
        }
    });

    if (!img || !container) return;

    // Prevent double-binding if initViewer is called twice
    if (container.dataset.viewerBound === 'true') {
        apply();
        return;
    }
    container.dataset.viewerBound = 'true';

    // Make sure events reach us even if CSS had pointer-events:none
    img.style.pointerEvents = 'auto';
    img.draggable = false;
    img.style.userSelect = 'none';
    img.style.webkitUserDrag = 'none';
    img.style.willChange = 'transform';
    img.style.transformOrigin = 'center center';

    // Mobile: stop browser’s native pan/zoom from stealing gestures
    container.style.touchAction = 'none';

    // Preserve any CSS transform the image already has (common cause of “zoom doesn’t work”)
    // We re-sample it on resize because computed matrix can change with layout.
    let cssTransform = '';
    function refreshCssTransform() {
        const prev = img.style.transform;
        img.style.transform = ''; // reveal pure CSS transform
        const t = getComputedStyle(img).transform;
        cssTransform = (t && t !== 'none') ? t : '';
        img.style.transform = prev;
    }

    function getContainerRect() {
        return container.getBoundingClientRect();
    }

    // Layout (untransformed) size of the displayed image (NOT affected by CSS transforms)
    function getBaseSize() {
        const w = img.offsetWidth || img.getBoundingClientRect().width || 1;
        const h = img.offsetHeight || img.getBoundingClientRect().height || 1;
        return { baseWidth: w, baseHeight: h };
    }

    function getMaxZoom() {
        const { baseWidth, baseHeight } = getBaseSize();
        const natW = img.naturalWidth || baseWidth || 1;
        const natH = img.naturalHeight || baseHeight || 1;

        const zx = natW / (baseWidth || 1);
        const zy = natH / (baseHeight || 1);

        // At least 4x, but allow up to 1:1 pixels or more when image was scaled down to fit
        return Math.max(4, zx, zy);
    }

    function clamp() {
        if (state.scale <= 1.001) {
            state.scale = 1;
            state.x = 0;
            state.y = 0;
            return;
        }

        const r = getContainerRect();
        const { baseWidth, baseHeight } = getBaseSize();

        const scaledW = baseWidth * state.scale;
        const scaledH = baseHeight * state.scale;

        const overflowX = scaledW - r.width;
        const overflowY = scaledH - r.height;

        if (overflowX > 0) {
            const maxX = overflowX / 2;
            state.x = Math.max(-maxX, Math.min(maxX, state.x));
        } else {
            state.x = 0;
        }

        if (overflowY > 0) {
            const maxY = overflowY / 2;
            state.y = Math.max(-maxY, Math.min(maxY, state.y));
        } else {
            state.y = 0;
        }
    }

    function apply() {
        // Keep existing CSS transform (if any) and append our pan/zoom
        const extra = `translate3d(${state.x}px, ${state.y}px, 0) scale(${state.scale})`;
        img.style.transform = (cssTransform ? (cssTransform + ' ') : '') + extra;

        const zoomed = state.scale > 1.01;
        img.classList.toggle('zoomed', zoomed);

        // Cursor feedback
        const cursor = zoomed ? (state.dragging ? 'grabbing' : 'grab') : 'zoom-in';
        container.style.cursor = cursor;
        img.style.cursor = cursor;

        // Hide nav buttons when zoomed (so panning doesn’t fight with nav)
        const nav = document.querySelector('.viewer-nav');
        if (nav) {
            nav.style.opacity = zoomed ? '0' : '';
            nav.style.pointerEvents = zoomed ? 'none' : '';
        }
    }

    function reset() {
        state.scale = 1;
        state.x = 0;
        state.y = 0;
        state.dragging = false;
        state.moved = false;
        apply();
    }

    function zoomAt(newScale, clientX, clientY) {
        const maxZ = getMaxZoom();
        newScale = Math.max(1, Math.min(maxZ, newScale));
        if (Math.abs(newScale - state.scale) < 0.0005) return;

        const rect = getContainerRect();
        const centerX = rect.left + rect.width / 2;
        const centerY = rect.top + rect.height / 2;

        const mouseX = clientX - centerX;
        const mouseY = clientY - centerY;

        // Convert mouse position to image-local coords at current scale
        const imgX = (mouseX - state.x) / state.scale;
        const imgY = (mouseY - state.y) / state.scale;

        // Update translate so the same image point stays under the cursor
        state.x = mouseX - imgX * newScale;
        state.y = mouseY - imgY * newScale;
        state.scale = newScale;

        clamp();
        apply();
    }

    // Make sure we capture CSS transform after the image actually lays out
    function onReady() {
        refreshCssTransform();
        reset();
    }
    if (img.complete) onReady();
    else img.addEventListener('load', onReady, { once: true });

    // Smooth wheel zoom (works better for trackpads too)
    container.addEventListener('wheel', (e) => {
        e.preventDefault();

        // Ctrl+wheel on some devices is “pinch” gesture; we still treat it as zoom.
        const zoomIntensity = 0.0015; // tweak feel
        const factor = Math.exp(-e.deltaY * zoomIntensity);
        zoomAt(state.scale * factor, e.clientX, e.clientY);
    }, { passive: false });

    // Click to toggle zoom (ignore click-after-drag)
    container.addEventListener('click', (e) => {
        if (state.moved) {
            state.moved = false;
            return;
        }

        const targetScale = (state.scale > 1.01) ? 1 : Math.min(2.5, getMaxZoom());
        zoomAt(targetScale, e.clientX, e.clientY);

        if (targetScale === 1) reset();
    });

    // Drag to pan (mouse)
    container.addEventListener('mousedown', (e) => {
        if (e.button !== 0) return;
        if (state.scale <= 1.01) return;

        e.preventDefault();
        state.dragging = true;
        state.moved = false;
        state.dragStartX = e.clientX;
        state.dragStartY = e.clientY;
        state.startX = state.x;
        state.startY = state.y;
        apply();
    });

    document.addEventListener('mousemove', (e) => {
        if (!state.dragging) return;

        const dx = e.clientX - state.dragStartX;
        const dy = e.clientY - state.dragStartY;

        if (Math.abs(dx) > 3 || Math.abs(dy) > 3) state.moved = true;

        state.x = state.startX + dx;
        state.y = state.startY + dy;

        clamp();
        apply();
    });

    document.addEventListener('mouseup', () => {
        if (!state.dragging) return;
        state.dragging = false;
        apply();
    });

    // Touch: pinch-zoom + pan + swipe nav when not zoomed
    let lastTouchDist = 0;
    let touchStartX = 0;
    let lastTap = 0;

    container.addEventListener('touchstart', (e) => {
        if (e.touches.length === 2) {
            e.preventDefault();
            lastTouchDist = Math.hypot(
                e.touches[0].clientX - e.touches[1].clientX,
                e.touches[0].clientY - e.touches[1].clientY
            );
        } else if (e.touches.length === 1) {
            touchStartX = e.touches[0].clientX;

            // Double tap zoom toggle
            const now = Date.now();
            if (now - lastTap < 300) {
                e.preventDefault();
                if (state.scale > 1.01) {
                    reset();
                } else {
                    const s = Math.min(2.5, getMaxZoom());
                    zoomAt(s, e.touches[0].clientX, e.touches[0].clientY);
                }
                lastTap = 0;
                return;
            }
            lastTap = now;

            // Start panning if zoomed
            if (state.scale > 1.01) {
                state.dragging = true;
                state.moved = false;
                state.dragStartX = e.touches[0].clientX;
                state.dragStartY = e.touches[0].clientY;
                state.startX = state.x;
                state.startY = state.y;
            }
        }
    }, { passive: false });

    container.addEventListener('touchmove', (e) => {
        if (e.touches.length === 2) {
            e.preventDefault();

            const dist = Math.hypot(
                e.touches[0].clientX - e.touches[1].clientX,
                e.touches[0].clientY - e.touches[1].clientY
            );

            const cx = (e.touches[0].clientX + e.touches[1].clientX) / 2;
            const cy = (e.touches[0].clientY + e.touches[1].clientY) / 2;

            if (lastTouchDist > 0) {
                zoomAt(state.scale * (dist / lastTouchDist), cx, cy);
            }
            lastTouchDist = dist;
        } else if (e.touches.length === 1 && state.dragging) {
            e.preventDefault();

            const dx = e.touches[0].clientX - state.dragStartX;
            const dy = e.touches[0].clientY - state.dragStartY;

            if (Math.abs(dx) > 3 || Math.abs(dy) > 3) state.moved = true;

            state.x = state.startX + dx;
            state.y = state.startY + dy;

            clamp();
            apply();
        }
    }, { passive: false });

    container.addEventListener('touchend', (e) => {
        if (e.touches.length === 0) {
            state.dragging = false;
            lastTouchDist = 0;

            // Swipe navigation only when NOT zoomed and no pan happened
            if (!state.moved && state.scale <= 1.01 && e.changedTouches && e.changedTouches[0]) {
                const diff = e.changedTouches[0].clientX - touchStartX;
                if (Math.abs(diff) > 50) {
                    if (diff < 0 && opts.nextUrl) window.location.href = opts.nextUrl;
                    else if (diff > 0 && opts.prevUrl) window.location.href = opts.prevUrl;
                }
            }
            state.moved = false;
            apply();
        }
    });

    // Recompute preserved CSS transform on resize (common source of broken zoom math/layout)
    window.addEventListener('resize', () => {
        refreshCssTransform();
        clamp();
        apply();
    }, { passive: true });

    // Initial paint
    refreshCssTransform();
    apply();
}

function goBack() {
    if (window.viewerOpts && window.viewerOpts.folderUrl) {
        window.location.href = window.viewerOpts.folderUrl;
    } else {
        window.location.href = '/';
    }
}
