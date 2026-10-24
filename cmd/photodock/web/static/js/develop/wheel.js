// ColorWheel picks hue (angle) and saturation (radius) for colour grading.
export class ColorWheel {
    constructor(label, callbacks) {
        this.cb = callbacks;
        this.hue = 0;
        this.sat = 0;
        this.drag = false;
        this.el = document.createElement('div');
        this.el.className = 'dv-wheel';
        this.el.innerHTML = '<span class="dv-wheel-title"></span><div class="dv-wheel-disc"><span class="dv-wheel-puck"></span></div><span class="dv-wheel-readout"></span>';
        this.el.querySelector('.dv-wheel-title').textContent = label;
        this.disc = this.el.querySelector('.dv-wheel-disc');
        this.puck = this.el.querySelector('.dv-wheel-puck');
        this.readout = this.el.querySelector('.dv-wheel-readout');
        this.disc.addEventListener('pointerdown', (e) => {
            this.drag = true;
            this.disc.setPointerCapture(e.pointerId);
            this.cb.onStart();
            this.update(e);
        });
        this.disc.addEventListener('pointermove', (e) => {
            if (this.drag) this.update(e);
        });
        const end = () => {
            if (!this.drag) return;
            this.drag = false;
            this.cb.onCommit(this.hue, this.sat);
        };
        this.disc.addEventListener('pointerup', end);
        this.disc.addEventListener('pointercancel', end);
        this.disc.addEventListener('dblclick', () => this.cb.onReset());
        this.set(0, 0);
    }

    update(e) {
        const r = this.disc.getBoundingClientRect();
        const dx = (e.clientX - (r.left + r.width / 2)) / (r.width / 2);
        const dy = (e.clientY - (r.top + r.height / 2)) / (r.height / 2);
        let hue = (Math.atan2(-dy, dx) * 180) / Math.PI;
        if (hue < 0) hue += 360;
        this.set(Math.round(hue), Math.round(Math.min(1, Math.hypot(dx, dy)) * 100));
        this.cb.onInput(this.hue, this.sat);
    }

    set(hue, sat) {
        this.hue = hue;
        this.sat = sat;
        const a = (hue * Math.PI) / 180;
        const rr = sat / 100;
        this.puck.style.left = `${50 + Math.cos(a) * rr * 50}%`;
        this.puck.style.top = `${50 - Math.sin(a) * rr * 50}%`;
        this.readout.textContent = sat ? `${hue}° · ${sat}` : '—';
    }

    setDisabled(d) {
        this.disc.classList.toggle('disabled', d);
    }
}
