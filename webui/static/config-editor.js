(function () {
  const clone = value => JSON.parse(JSON.stringify(value));
  const fields = {
    models: ['models', 'model', 'base_url', 'api_key', 'tool_choice', 'max_attempts', 'wake_routes'],
    behavior: ['temperature', 'max_tool_rounds', 'debounce', 'self_wake', 'context', 'history', 'persona_file', 'guide_file', 'multimodal', 'debug'],
    modules: ['modules'],
  };
  const equal = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  function buildPatch(section, baseline, draft) {
    const agent = {};
    for (const key of fields[section] || []) {
      if (!equal(baseline[key], draft[key]) && draft[key] !== undefined) agent[key] = clone(draft[key]);
    }
    return {agent};
  }
  function diffFields(before, patch, prefix = '') {
    const rows = [];
    for (const [key, value] of Object.entries(patch || {})) {
      const path = prefix ? prefix + '.' + key : key;
      const old = before?.[key];
      if (equal(old, value)) continue;
      if (value && typeof value === 'object' && !Array.isArray(value)) rows.push(...diffFields(old || {}, value, path));
      else {
        const sensitive = /api_key|token|password|secret|authorization/i.test(path);
        const display = v => sensitive ? '[已隐藏]' : JSON.stringify(v) ?? '(未设置)';
        rows.push({path, before: Array.isArray(old) ? old.length + ' 项' : display(old),
          after: Array.isArray(value) ? value.length + ' 项（连接密钥已隐藏）' : display(value)});
      }
    }
    return rows;
  }
  window.qmiConfigEditor = {clone, fields, buildPatch, diffFields,
    createDraft: view => clone(view.effective?.agent || view),
    hasChanges: (section, baseline, draft) => Object.keys(buildPatch(section, baseline, draft).agent).length > 0};
})();
