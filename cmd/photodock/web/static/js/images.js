(function() {
    // Widths match the server's on-demand variants. Never request the full original just to fit the screen.
    const variants = [
        ['w320', 320], ['w480', 480], ['w640', 640], ['w800', 800], ['w1024', 1024],
        ['w1280', 1280], ['w1600', 1600], ['w2048', 2048], ['large', 2560],
        ['w3200', 3200], ['w3840', 3840], ['w5120', 5120], ['w7680', 7680]
    ];

    function choose(cssWidth, sourceWidth, scale) {
        let needed = Math.ceil(cssWidth * (window.devicePixelRatio || 1) * (scale || 1));
        if (sourceWidth > 0) needed = Math.min(needed, sourceWidth);
        const variant = variants.find(v => v[1] >= needed) || variants[variants.length - 1];
        return { name: variant[0], width: variant[1] };
    }

    function url(smallURL, size) {
        return smallURL.replace('/thumb/small/', '/thumb/' + size + '/');
    }

    // Resolve only after the complete image can be painted, not while a JPEG is arriving top-down.
    function ready(img) {
        return new Promise((resolve, reject) => {
            function loaded() {
                if (!img.naturalWidth) { reject(new Error('Image unavailable')); return; }
                if (img.decode) img.decode().catch(() => {}).then(resolve);
                else resolve();
            }
            if (img.complete && img.src) {
                loaded();
            } else {
                img.addEventListener('load', loaded, { once: true });
                img.addEventListener('error', reject, { once: true });
            }
        });
    }

    window.PhotoImages = { choose, url, ready };
})();
