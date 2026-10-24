// PerfHud (toggled with P) shows the last preview's round trip and server
// stage breakdown plus rolling p50/p95 per kind of frame.
const KEY = 'photodock-develop-perf';
const WINDOW = 50;
const SHOWN_PARTS = 5;

function percentile(sorted, p) {
    if (!sorted.length) return 0;
    return sorted[Math.min(sorted.length - 1, Math.floor((p / 100) * sorted.length))];
}

export function parseServerTiming(header) {
    const out = {};
    if (!header) return out;
    header.split(',').forEach((entry) => {
        const [name, ...params] = entry.trim().split(';');
        const dur = params.map((p) => p.trim()).find((p) => p.startsWith('dur='));
        if (name && dur) out[name] = Number(dur.slice(4));
    });
    return out;
}

export class PerfHud {
    constructor(el) {
        this.el = el;
        this.samples = { drag: [], final: [], region: [] };
        this.last = null;
        let on = false;
        try { on = localStorage.getItem(KEY) === '1'; } catch (e) { on = false; }
        this.on = on;
        this.el.hidden = !on;
    }

    toggle() {
        this.on = !this.on;
        this.el.hidden = !this.on;
        try { localStorage.setItem(KEY, this.on ? '1' : '0'); } catch (e) { /* storage unavailable */ }
        this.render();
    }

    record(frame) {
        const kind = frame.region ? 'region' : frame.final ? 'final' : 'drag';
        const list = this.samples[kind];
        list.push(frame.rtt);
        if (list.length > WINDOW) list.shift();
        this.last = { ...frame, kind };
        if (this.on) this.render();
    }

    stats(kind) {
        const list = [...this.samples[kind]].sort((a, b) => a - b);
        if (!list.length) return `${kind} —`;
        return `${kind} p50 ${Math.round(percentile(list, 50))} / p95 ${Math.round(percentile(list, 95))} ms (n=${list.length})`;
    }

    render() {
        if (!this.on) return;
        const f = this.last;
        const lines = [];
        if (f) {
            const timing = f.timing || {};
            const server = timing.total ? `server ${Math.round(timing.total)} ms` : 'server —';
            lines.push(`${f.kind} · RT ${Math.round(f.rtt)} ms · ${server} · ${f.quality}`);
            const parts = Object.entries(timing)
                .filter(([k]) => k !== 'total')
                .sort((a, b) => b[1] - a[1])
                .slice(0, SHOWN_PARTS)
                .map(([k, v]) => `${k} ${v.toFixed(v < 10 ? 1 : 0)}`);
            if (parts.length) lines.push(parts.join(' · '));
        }
        lines.push(this.stats('drag'), this.stats('final'), this.stats('region'));
        this.el.textContent = lines.join('\n');
    }
}
