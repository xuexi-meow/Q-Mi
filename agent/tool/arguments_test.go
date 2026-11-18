package tool

import (
	"strings"
	"testing"
)

func TestValidateArgumentsEnforcesObjectSchema(t *testing.T) {
	contract := Tool{Name: "save", Parameters: ObjectParams(map[string]any{
		"count": IntProp("数量"),
	}, []string{"count"})}
	for _, raw := range []string{`null`, `[]`, `"draft"`, `{`, `{}`, `{"count":"one"}`, `{"count":1,"extra":true}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ValidateArguments(contract, raw); err == nil {
				t.Fatalf("accepted invalid arguments %s", raw)
			}
		})
	}
	raw := `{"count": 2}`
	if got, err := ValidateArguments(contract, raw); err != nil || string(got) != raw {
		t.Fatalf("valid arguments = %s, %v", got, err)
	}
}

func TestValidateArgumentsAllowsEmptyAndAsyncContracts(t *testing.T) {
	if got, err := ValidateArguments(Tool{Name: "finish"}, ""); err != nil || string(got) != "{}" {
		t.Fatalf("empty arguments = %s, %v", got, err)
	}
	item := Tool{Name: "download", Asyncable: true, Parameters: ObjectParams(nil, nil)}
	if _, err := ValidateArguments(item, `{"_async":true}`); err != nil {
		t.Fatal(err)
	}
	item.Asyncable = false
	if _, err := ValidateArguments(item, `{"_async":true}`); err == nil {
		t.Fatal("synchronous tool accepted _async")
	}
}

func TestValidateArgumentsRejectsRemoteSchemas(t *testing.T) {
	item := Tool{Name: "remote", Parameters: map[string]any{"$ref": "https://example.invalid/schema"}}
	if _, err := ValidateArguments(item, `{}`); err == nil || !strings.Contains(err.Error(), "remote tool schemas are disabled") {
		t.Fatalf("remote schema error = %v", err)
	}
}
