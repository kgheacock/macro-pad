import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { MAX_CHILDREN, REPO_MAX_CHILDREN } from '../src/render.js';
import { MAX_COUNT, appsCount } from '../src/plugins/apps.js';

const docGo = readFileSync(new URL('../../driver/layers/doc.go', import.meta.url), 'utf8');

function goConst(name) {
  const match = docGo.match(new RegExp(`^\\s*${name}\\s*=\\s*(\\d+)`, 'm'));
  assert.ok(match, `${name} is not in driver/layers/doc.go`);
  return Number(match[1]);
}

test('TestRepoCapEqualsMaxChildren', () => {
  assert.equal(REPO_MAX_CHILDREN, goConst('MaxChildren'));
});

test('TestAppsCountStopsAtTwenty', () => {
  assert.equal(MAX_CHILDREN, 20);
  assert.equal(MAX_COUNT, 20);
  assert.equal(appsCount({ INBOX_APPS_COUNT: '20' }), 20);
  assert.notEqual(appsCount({ INBOX_APPS_COUNT: '21' }), 21);
});
