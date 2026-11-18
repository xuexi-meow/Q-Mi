package tool_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"Q-Mi/agent/tool"
)

func TestObjectParamsJSON(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]any
		required   []string
		want       string
	}{
		{
			name: "nil parameters",
			want: `{"additionalProperties":false,"properties":{},"type":"object"}`,
		},
		{
			name:       "empty required",
			properties: map[string]any{},
			required:   []string{},
			want:       `{"additionalProperties":false,"properties":{},"type":"object"}`,
		},
		{
			name: "typed properties and required",
			properties: map[string]any{
				"url":     tool.StringProp("URL"),
				"count":   tool.IntProp("Count"),
				"enabled": tool.BoolProp("Enabled"),
			},
			required: []string{"url"},
			want:     `{"additionalProperties":false,"properties":{"count":{"description":"Count","type":"integer"},"enabled":{"description":"Enabled","type":"boolean"},"url":{"description":"URL","type":"string"}},"required":["url"],"type":"object"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tool.ObjectParams(tt.properties, tt.required))
			if err != nil {
				t.Fatalf("marshal schema: %v", err)
			}
			if string(data) != tt.want {
				t.Fatalf("schema = %s, want %s", data, tt.want)
			}
		})
	}
}

func TestParameterSchemaDefaultsMissingParameters(t *testing.T) {
	data, err := json.Marshal(tool.ParameterSchema(tool.Tool{}))
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	const want = `{"additionalProperties":false,"properties":{},"type":"object"}`
	if string(data) != want {
		t.Fatalf("schema = %s, want %s", data, want)
	}
}

func TestParameterSchemaPreservesDeclaredParameters(t *testing.T) {
	declared := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{"type": "string", "enum": []string{"read", "write"}},
		},
		"required":             []string{"mode"},
		"additionalProperties": false,
	}
	got := tool.ParameterSchema(tool.Tool{Parameters: declared})
	if !reflect.DeepEqual(got, declared) {
		t.Fatalf("schema = %#v, want declared schema %#v", got, declared)
	}
}

func TestParameterSchemaAsyncSupportsMissingParameters(t *testing.T) {
	schema := tool.ParameterSchema(tool.Tool{Asyncable: true}).(map[string]any)
	props := schema["properties"].(map[string]any)
	if schema["type"] != "object" || len(props) != 1 {
		t.Fatalf("async schema = %#v, want object with only _async", schema)
	}
	if prop, ok := props["_async"].(map[string]any); !ok || prop["type"] != "boolean" {
		t.Fatalf("_async = %#v, want boolean property", props["_async"])
	}
}

func TestParameterSchemaAsyncDoesNotMutateDefinition(t *testing.T) {
	declared := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string"},
		},
		"required":             []string{"url"},
		"additionalProperties": false,
	}
	schema := tool.ParameterSchema(tool.Tool{Parameters: declared, Asyncable: true}).(map[string]any)
	props := schema["properties"].(map[string]any)
	if !reflect.DeepEqual(props["url"], map[string]any{"type": "string"}) {
		t.Fatalf("url property lost: %#v", props)
	}
	if prop, ok := props["_async"].(map[string]any); !ok || prop["type"] != "boolean" {
		t.Fatalf("_async = %#v, want boolean property", props["_async"])
	}
	if schema["type"] != "object" || schema["additionalProperties"] != false ||
		!reflect.DeepEqual(schema["required"], []string{"url"}) {
		t.Fatalf("async schema lost original constraints: %#v", schema)
	}
	schema["additionalProperties"] = true
	delete(props, "url")

	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string"},
		},
		"required":             []string{"url"},
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(declared, want) {
		t.Fatalf("async schema generation mutated declared schema: %#v", declared)
	}
}
