# Agent Package Architecture Design

Date: 2026-11-12
Status: Draft for user review

## 1. Context

The `agent` implementation currently lives in one Go package and one directory. It contains orchestration, context construction, model protocol/client code, tool registration and execution, individual tool capabilities, event wiring, browser automation, and supporting state services. This makes the package easy to extend locally, but broadens the implicit dependency surface: every file can access every unexported symbol in the package.

The current inventory has 86 Go files including tests. `manager.go` is about 31 KB and `tools.go` is about 101 KB. Existing tool modules are registered in-process through `ToolModule` and `ToolRegistry`; they are statically assembled and do not provide runtime hot loading.

The change is intended to make responsibilities and dependencies easier to understand and test while preserving current behavior. Future additions may include custom tool modules and context contributors. Those should have stable compile-time interfaces, without committing to a dynamic plugin runtime now.

## 2. Goals

- Introduce real Go package boundaries for agent responsibilities instead of only renaming or regrouping files.
- Keep the existing `Q-Mi/agent` import path and user-visible behavior stable during migration.
- Make orchestration, context construction, model access, tool contracts, and tool implementations independently understandable and testable.
- Define extension contracts for tools and context contributors that can support future in-tree extensions.
- Ensure dependencies point toward stable contracts and do not form import cycles.
- Migrate incrementally, with behavior-preserving tests at every extraction.

## 3. Non-Goals

- No rewrite of agent behavior, prompts, message protocol, tool semantics, or model selection.
- No dynamic Go plugin loading, runtime hot swapping, third-party plugin sandbox, or MCP integration.
- No broad restructuring of unrelated packages such as `event`, `storage`, `api`, or `webui`.
- No one-shot migration of all files or speculative generic framework for arbitrary extensions.
- No requirement to split every internal service into its own package; components remain together until an independent boundary is demonstrated.

## 4. Target Package Boundaries

The target is a dependency-oriented structure. Names below describe responsibilities; exact file assignment is decided during implementation after checking symbol references.

```text
agent/                       Public facade, compatibility API, event wiring, composition root
agent/runtime/               Session lifecycle, inbox processing, conversation/tool loop
agent/context/               Turn/context contracts, context assembly, compaction, inspection views
agent/context/providers/      Built-in context contributors, registered by the composition root
agent/model/                 Chat protocol types, provider client, model routing/pool, usage
agent/tool/                  Tool and module contracts, execution contracts, narrow dependency ports
agent/tools/<capability>/    Built-in tool implementations grouped by capability
```

Candidate tool capability packages include QQ messaging and administration, memory and message lookup, browser, computer, QZone, stickers, and system utilities. These packages are extracted only where their dependency surface can be made explicit. Closely coupled implementations may remain together until a safe boundary is established.

`agent` remains the compatibility facade and composition root. It constructs runtime services, supplies built-in tool modules and context contributors, and connects external events. Existing callers should not need to import internal packages. The `Manager` is not moved as an initial mechanical step; orchestration is extracted after shared contracts and dependency directions are stable.

The `agent/tool` package owns contracts and registry/execution abstractions only. Capability packages implement those contracts. The composition root imports both the contract package and concrete capabilities and assembles the registry. The registry must not import concrete capability packages, preventing parent/child package cycles.

Similarly, `agent/context` defines context-contribution contracts and assembly behavior. Built-in contributors are provided by the composition root; the context package does not import its `providers` subpackage.

## 5. Dependency Rules

The intended dependency direction is:

```text
external callers
       |
       v
agent facade / composition root
       |
       +------> runtime ------> context contracts/assembly
       |          |             model contracts/client
       |          |             tool contracts/execution
       |          +-----------> storage and external ports
       |
       +------> built-in context providers
       +------> built-in tool capability packages
```

Rules:

- Internal packages do not import `Q-Mi/agent`; the facade may import internal packages.
- `runtime` depends on context, model, and tool abstractions, not on concrete tool capability packages.
- Tool capability packages depend on `agent/tool` contracts and narrow service interfaces. They do not receive or import the runtime manager.
- Context contributors depend on `agent/context` contracts and receive an immutable, scoped input for one turn.
- Packages do not depend on sibling implementation details through exported global variables or package-level singleton state.
- Shared types are placed with the responsibility that owns their invariants. A catch-all `common` package is explicitly avoided.
- If a proposed extraction creates an import cycle, the boundary must be revised by moving the smallest stable contract to the owning abstraction package, not by adding broad bidirectional callbacks.

## 6. Extension Contracts

### Tool modules

Tool implementations are registered through Go interfaces at process composition time. Each module declares its stable identity and returns its tools using a scoped dependency object composed from narrow interfaces. Tool metadata, schemas, ordering, and execution results remain behaviorally identical during the refactor.

The current static module list remains the source of built-in tools. Configuration continues to enable or disable supported modules using existing semantics. Runtime discovery, dynamic loading, and hot replacement are outside scope.

### Context contributors

Context assembly may accept multiple contributors. Each contributor returns explicitly identified context material for the current turn; the assembly layer controls deterministic ordering and inclusion in the final request. A contributor cannot mutate prior conversation history or replace the complete prompt.

Contributor inputs must be scoped to the current conversation/turn and treated as immutable. Contributor failures are logged with their identity and isolated where omission is safe. Required core context remains owned by the builder and cannot be silently omitted because an optional contributor failed.

The first implementation only defines and uses in-process Go interfaces for existing built-in context construction. It does not add a user-facing plugin configuration surface unless needed by an existing configuration behavior.

## 7. Runtime Data Flow

1. The `agent` facade receives or registers an external event and converts it into the existing incoming-message contract.
2. `runtime` routes the message to the correct conversation queue and owns wake-up, serialization, cancellation, and turn completion.
3. `context` builds a request from the current turn, bounded conversation history, built-in context contributors, and inspection metadata.
4. `model` sends the request through the currently configured candidate/routing behavior.
5. If the response includes tool calls, `runtime` resolves them through the tool registry and executes the selected capability with scoped dependencies and the current turn.
6. Tool results return to the same turn; the loop either continues or completes according to existing protocol behavior.
7. The facade delivers the final output through existing QQ/NapCat integrations.

At each step, conversation identity (`chat_type`, `chat_id`, account/self ID) remains explicit. No context, tool execution, or result may be shared across chats unless existing behavior explicitly requires it.

## 8. Migration Strategy

Migration is incremental and behavior-preserving. Each phase is independently buildable and testable.

1. **Baseline and ownership map:** Record package tests and key behavioral invariants. Map references for large and highly connected files before moving code. Establish a dependency check using Go package tooling.
2. **Model boundary:** Extract protocol/client and model-pool responsibilities into `agent/model`. Keep adapters in `agent` where needed to preserve existing API usage.
3. **Contract boundaries:** Establish the smallest stable tool and context contracts. Replace broad concrete dependencies with narrow interfaces only where required for extraction.
4. **Context boundary:** Move context building, compaction, and inspection-related data shaping into `agent/context`, while keeping orchestration decisions in runtime.
5. **Tool capabilities:** Split tool implementations by coherent capability. Migrate one capability at a time, preserving tool names, schemas, order, error results, permissions, and async behavior.
6. **Runtime boundary:** Move queue and conversation lifecycle into `agent/runtime` once it can depend only on the preceding abstractions. Keep the facade as the compatibility and composition layer.
7. **Cleanup:** Remove obsolete adapters only after all callers use the new boundaries; update architecture documentation and verify the final import graph.

The sequence may be adjusted if reference analysis shows a lower-risk extraction order, but the dependency rules and behavior-preservation requirements do not change. No phase combines unrelated event, storage, or WebUI restructuring.

## 9. Compatibility and Error Behavior

- Preserve the `Q-Mi/agent` package path and currently used public hooks during the migration.
- Preserve event names, wake semantics, message IDs, chat/account scoping, tool schemas and ordering, approval behavior, async task behavior, context message order, and model routing.
- Keep existing configuration keys and defaults. A new extension interface does not imply a new configuration setting.
- Tool and context failures must remain observable through existing error/logging paths. Optional context contributor failures may omit only that contributor's material; they may not cause another chat's content to be inserted.
- Do not convert recoverable optional capability failures into process-wide startup failures. Required dependency failures retain current startup semantics.

## 10. Verification

For each extraction:

- Run focused tests for the extracted package and the importing facade/runtime packages.
- Run `go test ./...` to catch repository-wide behavior and import errors.
- Run `go vet ./...` after package boundaries settle.
- Verify package import direction and absence of import cycles.
- Compare tool names, schemas, descriptions, and registry ordering against the pre-extraction behavior.
- Keep or add tests for chat isolation, context assembly order, tool dispatch/permissions, approvals, async completion, and facade compatibility where the moved code affects those contracts.

The final refactor is accepted only when the complete Go test suite passes and the package graph follows the dependency rules above. If environment restrictions prevent a verification command from running, report that explicitly rather than treating it as a pass.

## 11. Open Implementation Decisions

These decisions are intentionally deferred until symbol/reference analysis for each extraction:

- Exact public names and method signatures for context contributors and tool dependency ports.
- Whether the model protocol DTOs and provider client should share one package or use an internal protocol subpackage.
- Which tool capabilities have enough independent dependencies to become separate packages in the first migration.
- Which inspection types belong in context versus runtime; ownership follows the data invariants discovered during extraction.

These are implementation-level choices and do not change the approved target boundaries, dependency rules, or non-goals.
