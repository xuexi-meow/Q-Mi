package tool

import (
	"reflect"
	"strings"
	"testing"
)

type testContext struct{ Prefix string }

type testModule struct {
	name  string
	tools func(*testContext) []Tool
}

func (m testModule) Name() string                  { return m.name }
func (m testModule) OneLiner() string              { return "模块说明" }
func (m testModule) Intro() string                 { return "使用约束" }
func (m testModule) Tools(ctx *testContext) []Tool { return m.tools(ctx) }

func TestRegistryPreservesOrderAndPassesContext(t *testing.T) {
	r := NewRegistry[testContext](
		testModule{"core", func(ctx *testContext) []Tool {
			return []Tool{{Name: ctx.Prefix + "send"}, {Name: "finish"}}
		}},
		testModule{"memory", func(*testContext) []Tool { return []Tool{{Name: "search"}} }},
	)
	var names []string
	for _, item := range r.Build(&testContext{Prefix: "test_"}) {
		names = append(names, item.Name)
	}
	if want := []string{"test_send", "finish", "search"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("tool order/context = %v, want %v", names, want)
	}
	if got := r.Build(nil); got[0].Name != "send" {
		t.Fatalf("nil context must use zero value: %+v", got)
	}
}

func TestRegistryPromptDocsIncludesOrderedMetadataAndAsyncRules(t *testing.T) {
	r := NewRegistry[testContext](
		testModule{"core", func(*testContext) []Tool {
			return []Tool{{Name: "send", Description: "发送消息", Usage: "示例", Asyncable: true}}
		}},
		testModule{"memory", func(*testContext) []Tool { return []Tool{{Name: "search"}} }},
	)
	doc := r.PromptDocs()
	for _, want := range []string{"# 工具使用说明", "- core：模块说明（send）", "使用约束", "### send", "发送消息", "示例", `"_async": true`, "### search"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("missing %q in docs:\n%s", want, doc)
		}
	}
	if strings.Index(doc, "### send") >= strings.Index(doc, "### search") {
		t.Fatal("documentation order differs from registration order")
	}
	if doc != r.PromptDocs() {
		t.Fatal("documentation is not deterministic")
	}
}
