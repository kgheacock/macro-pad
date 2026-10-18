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
    const { id, parent } = fixture.removeLayerKey;
    assert.deepEqual(JSON.parse(encodeRemoveLayerKey(id, parent)), fixture);
  }
});

test('TestDecodeFixtures', () => {
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.layerChanged[0])), {
    kind: 'layerChanged',
    active: null,
  });
  assert.deepEqual(decodeMessage(JSON.stringify(fixtures.layerChanged[1])), {
    kind: 'layerChanged',
    active: 'inbox',
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
  assert.deepEqual(decodeMessage('{"kind":"setKeyState","setKeyState":{"keyIndex":1}}'), { kind: 'other' });
});
