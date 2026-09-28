const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Loads calibration.js with no document: the module publishes its pure
// helpers on window.Chassis.calibration and skips DOM wiring.
function loadHelpers() {
  const src = fs.readFileSync(path.join(__dirname, '..', 'static', 'calibration.js'), 'utf8');
  const window = {};
  vm.runInNewContext(src, { window, console });
  return window.Chassis.calibration;
}

const cal = loadHelpers();

// Objects built inside the vm realm have a foreign prototype; compare plain copies.
const eq = (actual, expected, msg) => assert.deepEqual(JSON.parse(JSON.stringify(actual)), expected, msg);
const base = { hSize: 100, vSize: 100, hOffset: 0, vOffset: 0 };

test('position mode moves one pixel / field line per press, Shift moves four', () => {
  eq(cal.nudge(base, 'position', 'right', 1), { ...base, hOffset: 1 });
  eq(cal.nudge(base, 'position', 'up', 1), { ...base, vOffset: -1 });
  eq(cal.nudge(base, 'position', 'left', 4), { ...base, hOffset: -4 });
  eq(cal.nudge(base, 'position', 'down', 4), { ...base, vOffset: 4 });
});

test('size mode: right/up grow, left/down shrink, 0.5% per press', () => {
  const g = { ...base, hSize: 90, vSize: 90 };
  assert.equal(cal.nudge(g, 'size', 'right', 1).hSize, 90.5);
  assert.equal(cal.nudge(g, 'size', 'left', 1).hSize, 89.5);
  assert.equal(cal.nudge(g, 'size', 'up', 1).vSize, 90.5);
  assert.equal(cal.nudge(g, 'size', 'down', 4).vSize, 88);
});

test('nudges clamp to the settings bounds', () => {
  assert.equal(cal.nudge(base, 'size', 'right', 4).hSize, 100);
  assert.equal(cal.nudge({ ...base, hSize: 80 }, 'size', 'left', 1).hSize, 80);
  assert.equal(cal.nudge({ ...base, hOffset: 71 }, 'position', 'right', 4).hOffset, 72);
  assert.equal(cal.nudge({ ...base, vOffset: -27 }, 'position', 'up', 4).vOffset, -28);
});

test('centre resets only the pair the mode adjusts', () => {
  const g = { hSize: 90, vSize: 92, hOffset: 5, vOffset: -2 };
  eq(cal.centred(g, 'position'), { hSize: 90, vSize: 92, hOffset: 0, vOffset: 0 });
  eq(cal.centred(g, 'size'), { hSize: 100, vSize: 100, hOffset: 5, vOffset: -2 });
});

test('normalize treats unset sizes as 100% and tidies values', () => {
  eq(cal.normalize({ hSize: 0, vSize: 92.54, hOffset: 3.4, vOffset: -99 }),
    { hSize: 100, vSize: 92.5, hOffset: 3, vOffset: -28 });
  eq(cal.normalize(undefined), base);
});

test('pictureBox places the draft in the raster diagram', () => {
  const box = cal.pictureBox({ hSize: 90, vSize: 90, hOffset: 72, vOffset: -24 }, { rasterWidth: 720, fieldLines: 240 });
  assert.equal(box.width, 90);
  assert.equal(box.height, 90);
  assert.ok(Math.abs(box.left - 15) < 1e-9, `left ${box.left}`);
  assert.ok(Math.abs(box.top - -5) < 1e-9, `top ${box.top}`);
});

test('previewQueue keeps one request in flight and sends only the latest queued draft', async () => {
  const sent = [];
  let release;
  const q = cal.previewQueue((g) => {
    sent.push(g.hOffset);
    return new Promise((resolve) => { release = resolve; });
  });
  const first = q.push({ ...base, hOffset: 1 });
  assert.equal(q.busy, true);
  q.push({ ...base, hOffset: 2 });
  q.push({ ...base, hOffset: 3 });
  eq(sent, [1]);
  release();
  await new Promise((r) => setImmediate(r));
  eq(sent, [1, 3], 'intermediate draft 2 is skipped');
  release();
  await first;
  assert.equal(q.busy, false);
  eq(sent, [1, 3]);
});

test('every end reason has operator text', () => {
  for (const reason of ['cast', 'stopped', 'timeout', 'error', 'save-failed']) {
    assert.ok(cal.END_TEXT[reason], reason);
  }
});
