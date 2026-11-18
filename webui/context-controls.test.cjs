const assert = require('node:assert/strict');
const {test} = require('node:test');
global.window = global;
global.localStorage = {getItem: () => null};
global.location = {hash: '#context'};
global.document = {hidden: false, querySelector: () => null};
require('./static/console.js');

function consoleApp() {
  const app = qmiConsole();
  app.page = 'context';
  app.authenticated = true;
  app.runtime = {available: true, phase: 'blocked'};
  app.contextPage = {source: 'working', version: 4, entries: []};
  app.$nextTick = fn => fn?.();
  app.icons = () => {};
  app.focusDialog = () => {};
  app.notify = () => {};
  return app;
}

test('editor saves original text, keeps drafts and pinned version after conflict', async () => {
  const app = consoleApp();
  app.contextDetail = {index: 3, text: '[redacted]'};
  app.contextDetailSource = 'working';
  app.contextDetailVersion = 4;
  const sent = [];
  app.requestJSON = async (path, options) => {
    sent.push({path, body: options?.body && JSON.parse(options.body)});
    if (path.startsWith('/api/context/edit?'))
      return {index: 3, version: 4, text: 'raw-original', editable: true, deletable: true, clear_reasoning: true};
    if (sent.at(-1).body.preview) return {version: 4, affected: [3], before: 1000, after: 900};
    const err = new Error('上下文版本已更新');
    err.status = 409;err.code = 'version_conflict';throw err;
  };
  await app.editContextEntry();
  assert.equal(app.contextEditor.draft, 'raw-original');
  assert.equal(app.contextActionError, '');
  app.contextEditor.draft = 'changed';
  app.contextEditor.clearReasoning = true;
  assert.equal(app.contextDirty, true);
  await app.saveContextEntry();
  assert.deepEqual(sent.at(-1).body, {
    version: 4, action: 'edit', index: 3, text: 'changed', clear_reasoning: true, preview: true
  });
  app.contextPage.version = 7;
  await app.commitContextAction();
  assert.equal(sent.at(-1).body.version, 4);
  assert.equal(app.contextEditor.draft, 'changed');
  assert.match(app.contextActionError, /版本已更新/);
  assert.ok(app.contextReview);
});

test('draft close guard and request snapshots never enter write flow', async () => {
  const app = consoleApp();
  app.contextDetail = {index: 2};
  app.contextEditor = {text: 'old', draft: 'edited', clearReasoning: false};
  global.confirm = () => false;
  app.closeOverlays();
  assert.ok(app.contextDetail);
  assert.equal(app.anyDirty(), true);
  app.contextPage.source = 'request';
  app.requestJSON = () => {throw new Error('read-only snapshot attempted a write');};
  await app.previewContextAction('reset');
  assert.equal(app.contextReview, null);
  assert.equal(app.contextWritable, false);
  app.contextPage.source = 'working';
  app.runtime.phase = 'requesting';
  assert.equal(app.contextWritable, false);
});

test('preview does not apply, successful operation refreshes working state', async () => {
  const app = consoleApp();
  const writes = [];
  app.requestJSON = async (path, options) => {
    if (path === '/api/context/action') {
      const body = JSON.parse(options.body);writes.push(body);
      return {version: body.preview ? 4 : 5, action: body.action, affected: [1, 2], before: 1000, after: 500, budget: 700, preview: body.preview};
    }
    if (path === '/api/runtime') return {available: true, phase: 'idle', working_version: 5};
    if (path.startsWith('/api/context?')) return {version: 5, source: 'working', entries: []};
    throw new Error('unexpected path ' + path);
  };
  await app.previewContextAction('clean');
  assert.equal(writes.length, 1);
  assert.equal(writes[0].preview, true);
  assert.equal(app.contextPage.version, 4);
  await app.commitContextAction();
  assert.equal(writes[1].preview, false);
  assert.equal(app.contextReview, null);
  assert.equal(app.contextPage.version, 5);
  assert.equal(app.runtime.phase, 'idle');
});
