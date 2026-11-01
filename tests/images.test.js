const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const code = fs.readFileSync(path.join(__dirname, '../cmd/photodock/web/static/js/images.js'), 'utf8');
const context = { window: { devicePixelRatio: 2 } };
vm.runInNewContext(code, context);
const images = context.window.PhotoImages;

test('responsive sizes use rendered width, pixel density, source size and zoom', () => {
    assert.equal(images.choose(120, 6000).name, 'w320');
    assert.equal(images.choose(300, 6000).name, 'w640');
    assert.equal(images.choose(300, 6000, 2).name, 'w1280');
    assert.equal(images.choose(1000, 400).name, 'w480');
    assert.equal(images.choose(5000, 30000).name, 'w7680');
    context.window.devicePixelRatio = 1;
    assert.equal(images.choose(300, 6000).name, 'w320');
    context.window.devicePixelRatio = 2;
});

test('variant URLs preserve edit versions and compare parameters', () => {
    assert.equal(images.url('/thumb/small/12?v=123abc', 'w640'), '/thumb/w640/12?v=123abc');
    assert.equal(images.url('/thumb/small/12?unedited=1', 'w1280'), '/thumb/w1280/12?unedited=1');
});

test('an image is not revealed before it is loaded and decoded', async () => {
    const listeners = {};
    let finishDecode;
    const img = {
        src: '/thumb/w640/12', complete: false, naturalWidth: 640,
        addEventListener(name, fn) { listeners[name] = fn; },
        decode() { return new Promise(resolve => { finishDecode = resolve; }); }
    };
    let revealed = false;
    const ready = images.ready(img).then(() => { revealed = true; });
    await Promise.resolve();
    assert.equal(revealed, false);
    listeners.load();
    await Promise.resolve();
    assert.equal(revealed, false);
    finishDecode();
    await ready;
    assert.equal(revealed, true);
});

test('failed images stay hidden and cached images can be displayed', async () => {
    const listeners = {};
    const failed = { src: '/bad', complete: false, naturalWidth: 0, addEventListener(name, fn) { listeners[name] = fn; } };
    const result = images.ready(failed);
    listeners.error(new Error('failed'));
    await assert.rejects(result, /failed/);
    await images.ready({ src: '/cached', complete: true, naturalWidth: 320 });
    await assert.rejects(images.ready({ src: '/bad', complete: true, naturalWidth: 0 }));
});
