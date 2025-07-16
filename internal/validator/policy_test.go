package validator

import (
	"encoding/json"
	"sort"
	"testing"
)

func TestPolicyViolations_AllChecks(t *testing.T) {
	def := map[string]any{
		"Comment": "test",
		"States": map[string]any{
			"GuardDuplicate": map[string]any{
				"Type":           "Task",
				"Resource":       "arn:aws:lambda:us-east-1:123:function:foo",
				"TimeoutSeconds": 10,
				"Retry":          []any{"foo"},
				"Next":           "ParseFile",
			},
			"ParseFile": map[string]any{
				"Type":           "Task",
				"Resource":       "arn:aws:lambda:us-east-1:123:function:bar",
				"TimeoutSeconds": 10,
				"Catch":          []any{"bar"},
				"Next":           "ArchiveMetrics",
			},
			"ArchiveMetrics": map[string]any{
				"Type":           "Task",
				"Resource":       "arn:aws:lambda:us-east-1:123:function:baz",
				"TimeoutSeconds": 10,
				"End":            true,
			},
			"MapState": map[string]any{
				"Type":           "Map",
				"MaxConcurrency": 2,
				"End":            true,
			},
		},
	}
	b, _ := json.Marshal(def)
	errs := PolicyViolations(b)
	// ArchiveMetrics should fail for missing Retry/Catch
	if len(errs) != 1 || errs[0].Error() != "ArchiveMetrics invokes Lambda without Retry or Catch" {
		t.Errorf("expected 1 error for ArchiveMetrics, got %v", errs)
	}
}

func TestPolicyViolations_MissingFields(t *testing.T) {
	def := map[string]any{
		"States": map[string]any{
			"GuardDuplicate": map[string]any{"Type": "Task"},
			"MapState":       map[string]any{"Type": "Map"},
			"TaskNoRetry":    map[string]any{"Type": "Task", "Resource": "lambda:foo"},
			"NoTransition":   map[string]any{"Type": "Task"},
		},
	}
	b, _ := json.Marshal(def)
	errs := PolicyViolations(b)
	if len(errs) != 8 {
		t.Errorf("expected 8 errors, got %d: %v", len(errs), errs)
	}
}

func TestPolicyViolations_BadJSON(t *testing.T) {
	errs := PolicyViolations([]byte("notjson"))
	if len(errs) != 1 {
		t.Error("expected 1 error for bad json")
	}
}

func TestExtractLambdas(t *testing.T) {
	def := map[string]any{
		"States": map[string]any{
			"A": map[string]any{"Resource": "arn:aws:lambda:us-east-1:123:function:foo"},
			"B": map[string]any{"Resource": "arn:aws:lambda:us-east-1:123:function:bar"},
			"C": map[string]any{"Resource": "arn:aws:stepfunctions:us-east-1:123:stateMachine:baz"},
		},
	}
	b, _ := json.Marshal(def)
	lambdas := ExtractLambdas(b)
	expected := []string{
		"arn:aws:lambda:us-east-1:123:function:foo",
		"arn:aws:lambda:us-east-1:123:function:bar",
	}
	sort.Strings(lambdas)
	sort.Strings(expected)
	for i := range expected {
		if lambdas[i] != expected[i] {
			t.Errorf("unexpected lambdas: %v", lambdas)
		}
	}
}
