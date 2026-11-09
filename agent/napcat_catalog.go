package agent

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed napcat_catalog.json
var napcatCatalogJSON []byte

type napcatAction struct {
	Name          string          `json:"name"`
	Aliases       []string        `json:"aliases"`
	Summary       string          `json:"summary"`
	Category      string          `json:"category"`
	ReadOnly      bool            `json:"readOnly"`
	InputSchema   json.RawMessage `json:"inputSchema"`
	Returns       string          `json:"returns"`
	ReturnsSchema json.RawMessage `json:"returnsSchema"`
	Examples      json.RawMessage `json:"examples"`
	Invariants    []string        `json:"invariants"`
	schema        *jsonschema.Schema
}

type napcatCatalog struct {
	Source   string          `json:"source"`
	Revision string          `json:"revision"`
	Actions  []*napcatAction `json:"actions"`
	byName   map[string]*napcatAction
}

var localNapcatCatalog = loadNapcatCatalog()

func loadNapcatCatalog() napcatCatalog {
	var c napcatCatalog
	if err := json.Unmarshal(napcatCatalogJSON, &c); err != nil {
		panic(fmt.Errorf("agent: invalid embedded NapCat catalog: %w", err))
	}
	c.byName = make(map[string]*napcatAction)
	sort.Slice(c.Actions, func(i, j int) bool { return c.Actions[i].Name < c.Actions[j].Name })
	for _, a := range c.Actions {
		var schema map[string]any
		if err := json.Unmarshal(a.InputSchema, &schema); err != nil {
			panic(err)
		}
		// The gateway accepts documented keys only; nested opaque feed/cursor
		// objects retain the upstream additionalProperties contract.
		schema["additionalProperties"] = false
		a.InputSchema, _ = json.Marshal(schema)
		compiler := jsonschema.NewCompiler()
		compiler.LoadURL = func(string) (io.ReadCloser, error) {
			return nil, fmt.Errorf("remote schema loading disabled")
		}
		url := "https://q-mi.invalid/napcat/" + a.Name
		if err := compiler.AddResource(url, bytes.NewReader(a.InputSchema)); err != nil {
			panic(err)
		}
		var err error
		a.schema, err = compiler.Compile(url)
		if err != nil {
			panic(fmt.Errorf("agent: invalid schema for %s: %w", a.Name, err))
		}
		for _, name := range append([]string{a.Name}, a.Aliases...) {
			if _, exists := c.byName[name]; exists {
				panic("agent: duplicate NapCat action: " + name)
			}
			c.byName[name] = a
		}
	}
	return c
}

func findNapcatAction(name string) (*napcatAction, error) {
	name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if name == "" {
		return nil, fmt.Errorf("缺少必填参数 action")
	}
	a, ok := localNapcatCatalog.byName[name]
	if !ok {
		return nil, fmt.Errorf("未知动作 %q；用 napcat_docs(query/category) 搜索目录，再用 action 查参数", truncateRunes(name, 80))
	}
	return a, nil
}

func napcatDocs(args json.RawMessage, admin bool) (string, error) {
	var p struct {
		Action   string `json:"action"`
		Query    string `json:"query"`
		Category string `json:"category"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	if p.Offset < 0 || p.Limit < 0 {
		return "", fmt.Errorf("offset 和 limit 不能为负数")
	}
	if p.Action != "" {
		a, err := findNapcatAction(p.Action)
		if err != nil {
			return "", err
		}
		policy, reason := napcatActionPolicy(a.Name)
		return marshalCompact(map[string]any{
			"action": a.Name, "aliases": a.Aliases, "desc": a.Summary, "category": a.Category,
			"policy": policy, "policy_reason": reason, "callable": policy == "allowed" || (policy == "admin" && admin),
			"input_schema": a.InputSchema, "returns": a.Returns, "returns_schema": a.ReturnsSchema,
			"examples": a.Examples, "invariants": a.Invariants,
			"local_constraints": napcatLocalConstraints(a.Name),
			"source":            localNapcatCatalog.Source, "revision": localNapcatCatalog.Revision,
		})
	}
	limit := p.Limit
	if limit == 0 {
		limit = 12
	}
	if limit > 25 {
		limit = 25
	}
	query := strings.ToLower(strings.TrimSpace(p.Query))
	category := strings.TrimSpace(p.Category)
	categories := map[string]int{}
	matches := make([]*napcatAction, 0)
	for _, a := range localNapcatCatalog.Actions {
		categories[a.Category]++
		if category != "" && category != a.Category {
			continue
		}
		haystack := strings.ToLower(a.Name + " " + strings.Join(a.Aliases, " ") + " " + a.Summary + " " + a.Category)
		if query == "" || strings.Contains(haystack, query) {
			matches = append(matches, a)
		}
	}
	start := min(p.Offset, len(matches))
	end := start + min(limit, len(matches)-start)
	rows := make([]map[string]any, 0, end-start)
	for _, a := range matches[start:end] {
		policy, reason := napcatActionPolicy(a.Name)
		desc := a.Summary
		if desc == "" {
			desc = a.Name
		}
		rows = append(rows, map[string]any{
			"action": a.Name, "desc": desc, "category": a.Category,
			"policy": policy, "policy_reason": reason, "callable": policy == "allowed" || (policy == "admin" && admin),
		})
	}
	var next *int
	if end < len(matches) {
		next = &end
	}
	return marshalCompact(map[string]any{
		"actions": rows, "total": len(matches), "offset": start, "next_offset": next, "categories": categories,
		"source": localNapcatCatalog.Source, "revision": localNapcatCatalog.Revision,
		"note": "目录是能力参考，不是待办。先搜索再按 action 查参数；优先已有封装工具。disabled 不可执行，admin 需程序授予权限；新增动作默认 disabled。",
	})
}
