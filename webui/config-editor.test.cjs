const assert = require('node:assert/strict');
global.window = global;
require('./static/config-editor.js');
const base = {models: {
  chat: {strategy: 'priority', candidates: [{name: 'primary', model: 'm', api_key: 'mask',
    tool_choice: 'required', context_window: 128000, weight: 2, cooldown: '3m',
    api_key_env: 'KEY', custom_field: 'keep'}]},
  background: {model: 'legacy', base_url: 'https://example.invalid'}
}, temperature: 0.8, context: {max_input_tokens: 64000}, modules: {browser: false}};
const draft = qmiConfigEditor.clone(base);
draft.models.chat.candidates[0].model = 'm2';
const patch = qmiConfigEditor.buildPatch('models', base, draft);
assert.equal(patch.agent.models.chat.candidates[0].context_window, 128000);
assert.equal(patch.agent.models.chat.candidates[0].custom_field, 'keep');
assert.equal(patch.agent.models.background.model, 'legacy');
assert.equal(base.models.chat.candidates[0].model, 'm');
draft.temperature = 0.4;
assert.deepEqual(qmiConfigEditor.buildPatch('behavior', base, draft), {agent: {temperature: 0.4}});
assert.equal(qmiConfigEditor.hasChanges('modules', base, draft), false);
const diff = qmiConfigEditor.diffFields({api_key:'secret-a'}, {api_key:'secret-b'});
assert.ok(!JSON.stringify(diff).includes('secret'));
console.log('Lossless configuration draft tests passed');
