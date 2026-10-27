import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { decodeMessage, encodeRemoveLayerKey, encodeSetLayerKey } from '../src/protocol.js';

const fixtures = JSON.parse(readFileSync(new URL('./fixtures/protocol.json', import.meta.url), 'utf8'));

test('TestEncodeMatchesFixtures', () => {
  for (const fixture of fixtures.setLayerKey) {
    assert.deepEqual(JSON.parse(encodeSetLayerKey(fixture.setLayerKey)), fixture);
  }
});

test('TestEncodeRemoveMatchesFixtures', () => {
  for (const fixture of fixtures.removeLayerKey) {
    const { id, parent, path } = fixture.removeLayerKey;
    assert.deepEqual(JSON.parse(encodeRemoveLayerKey(id, path ?? parent)), fixture);
  }
});

test('TestDecodeFixtures', () => {
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.layerChanged[0])), {
    kind: 'layerChanged',
    active: null,
    path: [],
  });
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.layerChanged[1])), {
    kind: 'layerChanged',
    active: 'inbox',
    path: ['inbox'],
  });
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.layerChanged[2])), {
    kind: 'layerChanged',
    active: 'inbox',
    path: ['inbox'],
  });
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.layerChanged[3])), {
    kind: 'layerChanged',
    active: 'app.pad',
    path: ['apps', 'app.pad'],
  });
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.childPressed[0])), {
    kind: 'childPressed',
    keyIndex: 1,
    parent: 'inbox',
    child: 'macro-pad.driver',
    path: ['inbox'],
  });
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.childPressed[1])), {
    kind: 'childPressed',
    keyIndex: 0,
    parent: 'app.pad',
    child: 'code',
    path: ['apps', 'app.pad'],
  });
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.event[0])), {
    kind: 'event',
    keyIndex: 2,
    type: 'press',
    timestamp: 123456,
  });
});

test('TestDecodeOddInput', () => {
  assert.equal(decodeMessage('not json'), null);
  assert.equal(decodeMessage('[1]'), null);
  assert.equal(decodeMessage('{"kind":"layerChanged","layerChanged":{"active":3}}'), null);
  assert.equal(decodeMessage('{"kind":"event"}'), null);
  assert.equal(decodeMessage('{"kind":"childPressed","childPressed":{"keyIndex":1,"parent":"inbox"}}'), null);
  assert.deepEqual(decodeMessage('{"kind":"setKeyState","setKeyState":{"keyIndex":1}}'), { kind: 'other' });
  assert.equal(decodeMessage('{"kind":"layerChanged","layerChanged":{"active":"a","path":"a"}}'), null);
  assert.equal(decodeMessage('{"kind":"layerChanged","layerChanged":{"active":"a","path":[1]}}'), null);
});

test('TestDecodeWithoutPath', () => {
  // A macropadd from before task 0086 sends no path.
  assert.deepEqual(decodeMessage('{"kind":"layerChanged","layerChanged":{"active":"inbox"}}'), {
    kind: 'layerChanged',
    active: 'inbox',
    path: ['inbox'],
  });
  assert.deepEqual(decodeMessage('{"kind":"childPressed","childPressed":{"keyIndex":1,"parent":"inbox","child":"c"}}'), {
    kind: 'childPressed',
    keyIndex: 1,
    parent: 'inbox',
    child: 'c',
    path: ['inbox'],
  });
});
