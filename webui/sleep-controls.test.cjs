const assert = require('node:assert/strict');
const {test} = require('node:test');
global.window = global;
global.localStorage = {getItem: () => null, removeItem: () => {}};
global.location = {hash: '#agent'};
global.document = {hidden: false};
require('./static/config-editor.js');
require('./static/console.js');

function app() {
  const a = qmiConsole();
  a.authenticated = true;a.page = 'agent';
  a.$nextTick = fn => fn?.();a.icons = () => {};a.notify = () => {};
  a.startPageRefresh = () => {};a.startEventWait = () => {};
  a.runtime = {sleep: {sleeping: true, pending_wake: false}};
  return a;
}

test('force wake needs no text and ignores repeated clicks while pending', async () => {
  const a = app(), calls = [];
  let complete;
  a.requestJSON = (path, options) => {
    calls.push({path, method: options?.method});
    if (path === '/api/sleep/wake') return new Promise(resolve => {complete = resolve;});
    return Promise.resolve({available: true, phase: 'idle', sleep: {sleeping: false}});
  };
  const pending = a.forceSleepWake();
  await a.forceSleepWake();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, '/api/sleep/wake');
  assert.equal(calls[0].method, 'POST');
  complete({sleeping: false});
  await pending;
  assert.equal(a.runtime.sleep.sleeping, false);
  assert.equal(a.busy.sleepWake, false);
});

test('failed wake retains sleeping state and displays error', async () => {
  const a = app();
  a.requestJSON = async () => {throw new Error('保存状态失败');};
  await a.forceSleepWake();
  assert.equal(a.runtime.sleep.sleeping, true);
  assert.match(a.errors.agent, /保存状态失败/);
});

test('state polling updates sleep without overwriting mood or wake draft', async () => {
  const a = app();
  a.moodDraft = '未保存的心情';a.moodBaseline = '旧心情';a.wakeDraft = '未发送';
  a.notes = [{index: 0, text: '已有笔记'}];
  a.requestJSON = async path => {
    assert.equal(path, '/api/runtime');
    return {sleep: {sleeping: false, wake_reason: 'messages'}};
  };
  await a.refreshPage();
  assert.equal(a.runtime.sleep.wake_reason, 'messages');
  assert.equal(a.moodDraft, '未保存的心情');
  assert.equal(a.wakeDraft, '未发送');
});

test('sleep config defaults stay clean and edits create behavior patch only', () => {
  const a = app();
  const baseline = a.normalizeAgent({effective: {agent: {sleep: {min_seconds: 0, enabled: null}}}});
  assert.equal(baseline.sleep.min_seconds, 180);
  assert.equal(baseline.sleep.enabled, true);
  const draft = structuredClone(baseline);
  assert.deepEqual(qmiConfigEditor.buildPatch('behavior', baseline, draft), {agent: {}});
  draft.sleep.threshold = 6;
  const patch = qmiConfigEditor.buildPatch('behavior', baseline, draft);
  assert.equal(patch.agent.sleep.threshold, 6);
  assert.deepEqual(qmiConfigEditor.buildPatch('models', baseline, draft), {agent: {}});
});
