package reqconv

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/d-kuro/kirocc/internal/anthropic"
)

func TestConvertTools_Basic(t *testing.T) {
	tools := []anthropic.Tool{
		{
			Name:        "get_weather",
			Description: "Get weather",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}},
				"required":   []any{"city"},
			},
		},
	}
	entries := ConvertTools(tools, nil)
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	spec := entries[0].ToolSpecification
	if spec.Name != "get_weather" || spec.Description != "Get weather" {
		t.Fatalf("unexpected spec: %+v", spec)
	}
}

func TestConvertTools_EmptyDescription(t *testing.T) {
	tools := []anthropic.Tool{{Name: "my_tool", InputSchema: map[string]any{}}}
	entries := ConvertTools(tools, nil)
	if entries[0].ToolSpecification.Description != "Tool: my_tool" {
		t.Fatalf("got %q", entries[0].ToolSpecification.Description)
	}
}

func TestConvertTools_LongDescription(t *testing.T) {
	longDesc := strings.Repeat("x", 50001)
	tools := []anthropic.Tool{{Name: "Bash", Description: longDesc, InputSchema: map[string]any{}}}
	entries := ConvertTools(tools, nil)
	if entries[0].ToolSpecification.Description != longDesc {
		t.Fatal("long description should be kept as-is")
	}
}

func TestConvertTools_LongNameShortened(t *testing.T) {
	longName := strings.Repeat("a", 65)
	tools := []anthropic.Tool{{Name: longName, InputSchema: map[string]any{}}}
	nameMap := NewToolNameMap()
	entries := ConvertTools(tools, nameMap)
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	short := entries[0].ToolSpecification.Name
	if len(short) > maxToolNameLen {
		t.Fatalf("shortened name still too long: %d chars", len(short))
	}
	if nameMap.Restore(short) != longName {
		t.Fatal("reverse mapping failed")
	}
}

func TestSanitizeJSONSchema_RemovesAdditionalProperties(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"x": map[string]any{"type": "string", "additionalProperties": true}},
	}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["additionalProperties"]; ok {
		t.Fatal("additionalProperties should be removed")
	}
}

func TestSanitizeJSONSchema_RemovesEmptyRequired(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{}}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["required"]; ok {
		t.Fatal("empty required should be removed")
	}
}

func TestSanitizeJSONSchema_KeepsNonEmptyRequired(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"x"}}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["required"]; !ok {
		t.Fatal("non-empty required should be kept")
	}
}

func TestSanitizeJSONSchema_ConstToEnum(t *testing.T) {
	schema := map[string]any{"const": "hello"}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["const"]; ok {
		t.Fatal("const should be removed")
	}
	enum, ok := got["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "hello" {
		t.Fatalf("expected enum: [hello], got %v", got["enum"])
	}
}

func TestSanitizeJSONSchema_Nil(t *testing.T) {
	got := SanitizeJSONSchema(nil)
	if got == nil {
		t.Fatal("should return empty map, not nil")
	}
}

func TestSanitizeJSONSchema_FlattensAnyOfEnums(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"anyOf": []any{
					map[string]any{"enum": []any{"pending", "in_progress", "completed"}, "type": "string"},
					map[string]any{"enum": []any{"deleted"}, "type": "string"},
				},
			},
		},
	}
	got := SanitizeJSONSchema(schema)
	props := got["properties"].(map[string]any)
	status := props["status"].(map[string]any)
	if _, ok := status["anyOf"]; ok {
		t.Fatal("anyOf should be flattened")
	}
	enum, ok := status["enum"].([]any)
	if !ok {
		t.Fatal("expected enum field")
	}
	if len(enum) != 4 {
		t.Fatalf("expected 4 enum values, got %d: %v", len(enum), enum)
	}
	if status["type"] != "string" {
		t.Fatalf("expected type string, got %v", status["type"])
	}
}

func TestSanitizeJSONSchema_AnyOfNullable_NoWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	old := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(old)

	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "null"},
		},
	}
	got := SanitizeJSONSchema(schema)

	if got["type"] != "string" {
		t.Fatalf("expected type string, got %v", got["type"])
	}
	if _, ok := got["anyOf"]; ok {
		t.Fatal("anyOf should be removed")
	}
	if buf.Len() > 0 {
		t.Fatalf("expected no warning for nullable anyOf, got: %q", buf.String())
	}
}

func TestSanitizeJSONSchema_OneOfNullable_NoWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	old := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(old)

	schema := map[string]any{
		"oneOf": []any{
			map[string]any{"type": "null"},
			map[string]any{"type": "integer", "description": "count"},
		},
	}
	got := SanitizeJSONSchema(schema)

	if got["type"] != "integer" {
		t.Fatalf("expected type integer, got %v", got["type"])
	}
	if got["description"] != "count" {
		t.Fatalf("expected description preserved, got %v", got["description"])
	}
	if buf.Len() > 0 {
		t.Fatalf("expected no warning for nullable oneOf, got: %q", buf.String())
	}
}

func TestSanitizeJSONSchema_AnyOfNullableMultiNonNull_PreservesBranches(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "integer"},
			map[string]any{"type": "null"},
		},
	}
	got := SanitizeJSONSchema(schema)
	// null branch dropped, remaining 2 branches preserved as anyOf array
	arr, ok := got["anyOf"].([]any)
	if !ok {
		t.Fatalf("expected anyOf array preserved, got %v", got)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 non-null branches, got %d", len(arr))
	}
}

func TestSanitizeJSONSchema_AnyOfNonEnum_PreservesBranches(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "number"},
		},
	}
	got := SanitizeJSONSchema(schema)
	arr, ok := got["anyOf"].([]any)
	if !ok {
		t.Fatalf("expected anyOf preserved, got %v", got)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(arr))
	}
}

func TestSanitizeJSONSchema_OneOfNonEnum_PreservesBranches(t *testing.T) {
	schema := map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "number"},
		},
	}
	got := SanitizeJSONSchema(schema)
	arr, ok := got["oneOf"].([]any)
	if !ok {
		t.Fatalf("expected oneOf preserved, got %v", got)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(arr))
	}
}

func TestSanitizeJSONSchema_AnyOfEnum_NoWarning(t *testing.T) {
	// When all branches are enum-based, no warning should be logged.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	old := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(old)

	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"enum": []any{"a"}, "type": "string"},
			map[string]any{"enum": []any{"b"}, "type": "string"},
		},
	}
	SanitizeJSONSchema(schema)

	if buf.Len() > 0 {
		t.Fatalf("expected no warning for enum-based anyOf, got: %q", buf.String())
	}
}

func TestSanitizeJSONSchema_AnyOfNonEnum_PreservesAllBranches(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string", "description": "a string"},
			map[string]any{"type": "number"},
		},
	}
	got := SanitizeJSONSchema(schema)
	arr, ok := got["anyOf"].([]any)
	if !ok {
		t.Fatal("anyOf should be preserved as array")
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(arr))
	}
	first := arr[0].(map[string]any)
	if first["type"] != "string" {
		t.Fatalf("first branch type = %v", first["type"])
	}
}

func TestSanitizeJSONSchema_AnyOfConstBranches(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"const": "A"},
			map[string]any{"const": "B"},
			map[string]any{"const": "C"},
		},
	}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["anyOf"]; ok {
		t.Fatal("anyOf should be flattened")
	}
	enum, ok := got["enum"].([]any)
	if !ok {
		t.Fatalf("expected enum field, got %v", got)
	}
	if len(enum) != 3 {
		t.Fatalf("expected 3 enum values, got %d: %v", len(enum), enum)
	}
}

func TestSanitizeJSONSchema_AnyOfMixedTypes_NoType(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"enum": []any{"hello"}, "type": "string"},
			map[string]any{"enum": []any{42}, "type": "integer"},
		},
	}
	got := SanitizeJSONSchema(schema)
	enum, ok := got["enum"].([]any)
	if !ok {
		t.Fatalf("expected enum field, got %v", got)
	}
	if len(enum) != 2 {
		t.Fatalf("expected 2 enum values, got %d: %v", len(enum), enum)
	}
	if _, ok := got["type"]; ok {
		t.Fatal("type should be omitted for mixed-type enums")
	}
}

func TestSanitizeJSONSchema_AllOfMerged(t *testing.T) {
	schema := map[string]any{
		"allOf": []any{
			map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
			map[string]any{"required": []any{"a"}},
		},
	}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["allOf"]; ok {
		t.Fatal("allOf should be removed")
	}
	if got["type"] != "object" {
		t.Fatalf("expected type object, got %v", got["type"])
	}
	req, ok := got["required"].([]any)
	if !ok || len(req) != 1 {
		t.Fatalf("expected required [a], got %v", got["required"])
	}
}

func TestSanitizeJSONSchema_RemovesValidationKeywords(t *testing.T) {
	// These are still removed:
	removed := []string{
		"minLength", "maxLength",
		"minimum", "maximum",
		"minItems", "maxItems",
		"uniqueItems", "multipleOf",
		"not",
	}
	for _, kw := range removed {
		schema := map[string]any{"type": "string", kw: "value"}
		got := SanitizeJSONSchema(schema)
		if _, ok := got[kw]; ok {
			t.Fatalf("%q should be removed", kw)
		}
	}
	// These are now preserved:
	preserved := []string{"format", "pattern"}
	for _, kw := range preserved {
		schema := map[string]any{"type": "string", kw: "value"}
		got := SanitizeJSONSchema(schema)
		if _, ok := got[kw]; !ok {
			t.Fatalf("%q should be preserved", kw)
		}
	}
}

func TestSanitizeJSONSchema_RemovesDollarSchema(t *testing.T) {
	schema := map[string]any{"type": "object", "$schema": "http://json-schema.org/draft-07/schema#"}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["$schema"]; ok {
		t.Fatal("$schema should be removed")
	}
}

func TestSanitizeJSONSchema_RemovesPatternProperties(t *testing.T) {
	schema := map[string]any{"type": "object", "patternProperties": map[string]any{}}
	got := SanitizeJSONSchema(schema)
	if _, ok := got["patternProperties"]; ok {
		t.Fatal("patternProperties should be removed")
	}
}

func TestSanitizeJSONSchema_AnyOfPreservedWithType(t *testing.T) {
	// anyOf preserved as-is alongside the outer "type" field.
	schema := map[string]any{
		"type": "object",
		"anyOf": []any{
			map[string]any{"type": "string", "description": "a string"},
			map[string]any{"type": "number"},
		},
	}
	got := SanitizeJSONSchema(schema)
	if got["type"] != "object" {
		t.Fatalf("outer type should be preserved, got %v", got["type"])
	}
	arr, ok := got["anyOf"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("anyOf should be preserved with 2 branches, got %v", got["anyOf"])
	}
}
