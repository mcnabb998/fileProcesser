package validator

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// PolicyViolations runs policy checks on the state machine definition bytes.
func PolicyViolations(data []byte) []error {
	var def struct {
		Comment string                    `json:"Comment"`
		States  map[string]map[string]any `json:"States"`
	}
	if err := json.Unmarshal(data, &def); err != nil {
		return []error{err}
	}
	var errs []error
	// top-level comment presence
	if def.Comment == "" {
		errs = append(errs, fmt.Errorf("missing Comment"))
	}
	for name, state := range def.States {
		t, _ := state["Type"].(string)
		// TimeoutSeconds check for key states
		if name == "GuardDuplicate" || name == "ParseFile" || name == "ArchiveMetrics" {
			if _, ok := state["TimeoutSeconds"]; !ok {
				errs = append(errs, fmt.Errorf("%s missing TimeoutSeconds", name))
			}
		}
		// Map MaxConcurrency
		if t == "Map" {
			if _, ok := state["MaxConcurrency"]; !ok {
				errs = append(errs, fmt.Errorf("%s missing MaxConcurrency", name))
			}
		}
		// Lambda Retry/Catch
		if t == "Task" {
			if res, ok := state["Resource"].(string); ok && strings.Contains(res, "lambda") {
				if _, hasRetry := state["Retry"]; !hasRetry {
					if _, hasCatch := state["Catch"]; !hasCatch {
						errs = append(errs, fmt.Errorf("%s invokes Lambda without Retry or Catch", name))
					}
				}
			}
		}
		// transitions
		if _, ok := state["End"]; !ok {
			if _, ok := state["Next"]; !ok {
				errs = append(errs, fmt.Errorf("%s missing Next or End", name))
			}
		}
	}
	return errs
}

// ExtractLambdas returns lambda function names referenced by the definition.
func ExtractLambdas(data []byte) []string {
	var def struct{ States map[string]map[string]any }
	if err := json.Unmarshal(data, &def); err != nil {
		return nil
	}
	var out []string
	for _, s := range def.States {
		if res, ok := s["Resource"].(string); ok {
			if strings.Contains(res, "lambda") {
				out = append(out, path.Base(res))
			}
		}
	}
	return out
}
