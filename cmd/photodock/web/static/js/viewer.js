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
    if (opts.prevOriginal) new Image().src = opts.prevOriginal;
    if (opts.nextOriginal) new Image().src = opts.nextOriginal;

    // Keyboard navigation
    document.addEventListener('keydown', (e) => {
        const tag = e.target && e.target.tagName;
        if (tag === 'INPUT' || tag === 'TEXTAREA') return;

        if (e.key === 'ArrowLeft' && opts.prevUrl) {
            window.location.href = opts.prevUrl;
        } else if (e.key === 'ArrowRight' && opts.nextUrl) {
            window.location.href = opts.nextUrl;
        } else if (e.key === 'r' || e.key === 'R') {
            window.location.href = '/random';
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
    img.style.transformOrigin = '0 0';

    // Mobile: stop browser’s native pan/zoom from stealing gestures
    container.style.touchAction = 'none';

    // Preserve any existing CSS transform (re-sampled on resize since the computed matrix can change).
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

    let base = { left: 0, top: 0, width: 1, height: 1 };

    function measureBase() {
        const prevTransition = img.style.transition;
        const prevTransform = img.style.transform;
        img.style.transition = 'none';
        img.style.transform = 'none';
        const r = img.getBoundingClientRect();
        base = { left: r.left, top: r.top, width: r.width || 1, height: r.height || 1 };
        img.style.transform = prevTransform;
        img.style.transition = prevTransition;
    }

    function getMaxZoom() {
        const natW = img.naturalWidth || base.width || 1;
        const natH = img.naturalHeight || base.height || 1;
        const zx = natW / (base.width || 1);
        const zy = natH / (base.height || 1);
        return Math.max(4, zx, zy);
    }

    function clampAxis(t, baseStart, baseSize, cStart, cSize, scale) {
        const scaled = baseSize * scale;
        if (scaled <= cSize + 0.5) {
            return (cStart + cSize / 2) - (baseStart + scaled / 2);
        }
        const min = cStart + cSize - baseStart - scaled;
        const max = cStart - baseStart;
        return Math.max(min, Math.min(max, t));
    }

    function clamp() {
        if (state.scale <= 1.001) state.scale = 1;
        const c = getContainerRect();
        state.x = clampAxis(state.x, base.left, base.width, c.left, c.width, state.scale);
        state.y = clampAxis(state.y, base.top, base.height, c.top, c.height, state.scale);
    }

    function apply() {
        img.style.transform = `translate3d(${state.x}px, ${state.y}px, 0) scale(${state.scale})`;

        const zoomed = state.scale > 1.01;
        img.classList.toggle('zoomed', zoomed);

        const cursor = zoomed ? (state.dragging ? 'grabbing' : 'grab') : 'zoom-in';
        container.style.cursor = cursor;
        img.style.cursor = cursor;

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
        measureBase();
        apply();
    }

    function zoomAt(newScale, clientX, clientY) {
        if (state.scale <= 1.001) measureBase();

        const maxZ = getMaxZoom();
        newScale = Math.max(1, Math.min(maxZ, newScale));
        if (Math.abs(newScale - state.scale) < 0.0005) return;

        const f = (clientX - base.left - state.x) / (base.width * state.scale);
        const g = (clientY - base.top - state.y) / (base.height * state.scale);

        state.x = clientX - base.left - f * base.width * newScale;
        state.y = clientY - base.top - g * base.height * newScale;
        state.scale = newScale;

        clamp();
        apply();
    }

    function upgradeToOriginal() {
        const orig = img.dataset.original;
        if (!orig || img.dataset.upgraded) return;
        img.dataset.upgraded = 'pending';

        const hi = new Image();
        hi.src = orig;

        // Decode off-screen before swapping so the visible <img> paints from cache without blanking.
        const swap = () => {
            img.src = orig;
            img.dataset.upgraded = 'true';
        };
        if (hi.decode) {
            hi.decode().then(swap).catch(() => { img.dataset.upgraded = ''; });
        } else {
            hi.onload = swap;
            hi.onerror = () => { img.dataset.upgraded = ''; };
        }
    }

    function onReady() {
        reset();
        upgradeToOriginal();
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
        measureBase();
        clamp();
        apply();
    }, { passive: true });

    initCompare(img);

    // Initial paint
    measureBase();
    apply();
}

// initCompare shows the unedited photo while a compare button or B is held, then restores the view.
function initCompare(img) {
    const btns = Array.from(document.querySelectorAll('.develop-compare-btn'));
    if (!btns.length || !img) return;
    const before = btns[0].dataset.before;
    let saved = null;
    new Image().src = before;

    const show = () => {
        if (saved !== null) return;
        saved = img.src;
        img.src = before;
        btns.forEach((b) => b.classList.add('active'));
    };
    const hide = () => {
        if (saved === null) return;
        img.src = saved;
        saved = null;
        btns.forEach((b) => b.classList.remove('active'));
    };

    btns.forEach((btn) => {
        btn.addEventListener('pointerdown', (e) => {
            e.preventDefault();
            btn.setPointerCapture(e.pointerId);
            show();
        });
        ['pointerup', 'pointercancel', 'lostpointercapture'].forEach((t) => btn.addEventListener(t, hide));
        btn.addEventListener('contextmenu', (e) => e.preventDefault());
    });
    document.addEventListener('keydown', (e) => {
        const tag = e.target && e.target.tagName;
        if ((e.key === 'b' || e.key === 'B') && tag !== 'INPUT' && tag !== 'TEXTAREA' && !e.repeat) show();
    });
    document.addEventListener('keyup', (e) => {
        if (e.key === 'b' || e.key === 'B') hide();
    });
    window.addEventListener('blur', hide);
}

function goBack() {
    if (window.viewerOpts && window.viewerOpts.folderUrl) {
        window.location.href = window.viewerOpts.folderUrl;
    } else {
        window.location.href = '/';
    }
}
