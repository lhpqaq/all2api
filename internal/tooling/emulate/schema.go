package emulate

import (
	"fmt"
	"sort"
	"strings"
)

func compactSchema(schema map[string]any) string {
	propsAny, ok := schema["properties"].(map[string]any)
	if !ok || len(propsAny) == 0 {
		return ""
	}
	requiredSet := map[string]bool{}
	if reqList, ok := schema["required"].([]any); ok {
		for _, v := range reqList {
			if s, ok := v.(string); ok {
				requiredSet[s] = true
			}
		}
	}

	keys := make([]string, 0, len(propsAny))
	for k := range propsAny {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		t := "any"
		if prop, ok := propsAny[k].(map[string]any); ok {
			t = schemaType(prop)
		}
		marker := "?"
		if requiredSet[k] {
			marker = "!"
		}
		parts = append(parts, fmt.Sprintf("%s%s:%s", k, marker, t))
	}
	return strings.Join(parts, ",")
}

func schemaType(prop map[string]any) string {
	if t, ok := prop["type"].(string); ok {
		switch t {
		case "string":
			return "str"
		case "number":
			return "num"
		case "integer":
			return "int"
		case "boolean":
			return "bool"
		case "object":
			return "obj"
		case "array":
			return "arr"
		default:
			return t
		}
	}
	return "any"
}
