const assert = require('node:assert/strict');

global.window = global;
global.localStorage = {getItem: () => null};
global.location = {hash: ''};
global.document = {hidden: false};
require('./static/console.js');

async function run() {
  const app = qmiConsole();
  app.authenticated = true;
  app.page = 'activity';
  app.followEvents = false;
  const responses = [
    {cursor: 4, reset: false, events: [{
      id: 1, cursor: 4, run_id: 9, kind: 'model_stream', state: 'generating',
      chat_type: 'group', chat_id: 88, reasoning: '先看前文', tool_calls: [{index: 0, arguments: '{"q":'}]
    }]},
    {cursor: 6, reset: false, events: [{
      id: 1, cursor: 6, run_id: 9, kind: 'model_stream', state: 'completed',
      chat_type: 'group', chat_id: 88, reasoning: '先看前文再查', tool_calls: [{index: 0, arguments: '{"q":"猫"}'}]
    }]},
    {cursor: 4, reset: false, events: [{
      id: 1, cursor: 4, run_id: 9, kind: 'model_stream', state: 'generating',
      chat_type: 'group', chat_id: 88, reasoning: '旧响应', tool_calls: [{index: 0, arguments: '{"q":'}]
    }]}
  ];
  const paths = [];
  app.requestJSON = async path => {
    paths.push(path);
    return responses.shift();
  };

  await app.loadEvents();
  await app.loadEvents();
  await app.loadEvents();
  assert.equal(app.events.length, 1);
  assert.equal(app.events[0].reasoning, '先看前文再查');
  assert.equal(app.events[0].tool_calls[0].arguments, '{"q":"猫"}');
  assert.equal(app.eventCursor, 6);
  assert.ok(paths.every(path => path.startsWith('/api/events/wait?')));

  app.requestJSON = async () => ({
    cursor: 8, reset: true,
    events: [{id: 12, cursor: 8, kind: 'output', text: 'reset snapshot'}]
  });
  await app.loadEvents();
  assert.deepEqual(app.events.map(event => event.id), [12]);
  assert.equal(app.eventCursor, 8);

  let waitSignal;
  global.fetch = async (_path, options) => {
    waitSignal = options.signal;
    return new Promise((_resolve, reject) => waitSignal.addEventListener('abort', () => {
      const error = new Error('aborted');
      error.name = 'AbortError';
      reject(error);
    }));
  };
  app.startEventWait();
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(waitSignal);
  app.stopEventWait();
  assert.equal(waitSignal.aborted, true);
}

run().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
