// Run manually: node --test internal/chassis/testdata/setup.behavior.test.js
// (Not wired into Makefile/CI — mirrors the existing chassis JS test convention.)
const { test } = require('node:test');
const assert = require('node:assert');

// Minimal applyStatus extracted to validate checklist/Finish toggling logic.
function applyStatus(doc, st) {
  const steps = doc.steps;
  steps[0].done = !!st.hostSet;
  steps[1].done = !!st.sourceEnabled;
  doc.finish.disabled = !st.complete;
}

test('Finish disabled until complete; steps tick independently', () => {
  const doc = { steps: [{ done: false }, { done: false }], finish: { disabled: true } };
  applyStatus(doc, { hostSet: true, sourceEnabled: false, complete: false });
  assert.equal(doc.steps[0].done, true);
  assert.equal(doc.steps[1].done, false);
  assert.equal(doc.finish.disabled, true);
  applyStatus(doc, { hostSet: true, sourceEnabled: true, complete: true });
  assert.equal(doc.finish.disabled, false);
});
