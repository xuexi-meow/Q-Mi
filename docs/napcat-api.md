# SnowLuma API Access

Common operations keep their existing tools. Long-tail operations use two tools:

1. `napcat_docs({"query":"qzone","limit":5})` searches the local catalog.
2. `napcat_docs({"action":"get_qzone_public_feeds"})` returns the input schema,
   examples, result schema, upstream invariants and local policy.
3. `napcat_api({"action":"get_qzone_public_feeds","params":{}})` executes it.

Omitting documentation arguments returns categories and a short first page.
Use `category`, `offset` and `limit` to browse; follow `next_offset` until null.
Search matches names, aliases, descriptions and category names. Lists default
to 12 entries and never exceed 25 entries per page.

## Boundaries

- `allowed`: available to the autonomous account.
- `admin`: available only when the program grants administrator context.
- `disabled`: unavailable even in administrator context.
- Unknown or newly added actions are disabled until explicitly reviewed.
- Credentials, database keys, raw protocol packets, process control, streaming
  and upstream placeholder actions are not exposed for execution.
- Group renaming and group announcements now require administrator context
  through both wrappers and the generic gateway.
- Parameters are checked against the embedded JSON Schema. Undocumented
  top-level keys are rejected; opaque nested Qzone feed objects are preserved.
  Explicit false/zero values are not omitted.
- Ban duration is capped at 30 days; message history is capped at 20 entries.
  File uploads reuse existing local-path restrictions and path mapping.
- Authored messages, forward nodes/previews, Qzone content/comments and voice
  text pass the shared context-prefix check. Third-party Qzone feed metadata is
  not treated as authored outgoing text.
- Large gateway results return `truncated: true` and a bounded preview instead
  of silently pretending to be complete. Use pagination or a dedicated wrapper.
- Schema validation does not implement every prose-only upstream invariant.
  Read the action's examples/invariants; the running server remains authoritative
  about permissions, cross-field validity and actual availability.

The directory is reference material, not an activity checklist. Only a short
capability/navigation description lives in the permanent prompt; action details
enter context only when requested.

## Updating

`agent/napcat_catalog.json` is a build-time snapshot of the official generated
MCP catalog, with its source URL and commit revision recorded inside it.
No remote documentation is fetched at runtime.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/update_napcat_catalog.ps1 -Revision <reviewed-commit>
go test ./...
```

The initial snapshot contains 222 canonical actions plus aliases from revision
`f2ea3354f8bbe8672cbb4ae6a2b4e1fbeff44faf`. Review changes to action schemas
and policies in `agent/napcat_gateway.go` before enabling new actions.
Rebuild and restart to load code/catalog changes. Existing prompt hot-reloading
is unchanged.

Verification uses local HTTP test servers, not a real QQ account. A running
server may implement a different API revision or lack particular capabilities.
