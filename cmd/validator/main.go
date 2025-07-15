package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/your-org/file-processor-sample/internal/validator"
)

func main() {
	roots := []string{"sfn", "infra", "cmd"}
	aslFiles, err := validator.DiscoverASL(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover ASL: %v\n", err)
	}

	// check profiles for expected ASL
	profiles, _ := filepath.Glob(filepath.Join("sample-profiles", "*.json"))
	taskID := 1
	var missing []string
	for _, p := range profiles {
		base := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		expected := filepath.Join("build", base+".asl.json")
		if _, err := os.Stat(expected); err != nil {
			task := filepath.Join("tasks", fmt.Sprintf("TASK-SFN-%03d_%s.md", taskID, base))
			createTask(task, expected, []error{fmt.Errorf("missing ASL for profile %s", base)})
			taskID++
			missing = append(missing, base)
		}
	}

	reportDir := filepath.Join("audit")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", reportDir, err)
	}
	reportPath := filepath.Join(reportDir, "Project-Status-Report.md")

	tasksDir := "tasks"
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", tasksDir, err)
	}

	var table string
	var hasErrors bool
	for _, f := range aslFiles {
		errs := validator.ValidateASL(f)
		data, err := os.ReadFile(f)
		if err != nil {
			errs = append(errs, fmt.Errorf("read: %w", err))
		} else {
			errs = append(errs, validator.PolicyViolations(data)...)
		}
		if len(errs) > 0 {
			hasErrors = true
			slug := filepath.Base(f)
			slug = slug[:len(slug)-len(filepath.Ext(slug))]
			taskPath := filepath.Join(tasksDir, fmt.Sprintf("TASK-SFN-%03d_%s.md", taskID, slug))
			taskID++
			createTask(taskPath, f, errs)
			table += fmt.Sprintf("| %s | ❌ |\n", f)
		} else {
			table += fmt.Sprintf("| %s | ✅ |\n", f)
		}
	}
	if table == "" {
		table = "_No Step Functions found_\n"
	} else {
		table = "| File | Status |\n|------|--------|\n" + table
	}
	if len(missing) > 0 {
		table += "\n\nMissing ASL files: " + strings.Join(missing, ", ")
	}

	badge := ""
	if !hasErrors && len(aslFiles) > 0 {
		badge = "\n\nState Machine Integrity ✅"
	}

	content := fmt.Sprintf("## Step-Function Audit\n%s%s\n", table, badge)

	if err := os.WriteFile(reportPath, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
	}
}

func createTask(path, file string, errs []error) {
	var b string
	b += fmt.Sprintf("# Step Function issue for %s\n\n", file)
	b += "## Problems\n"
	for _, e := range errs {
		b += fmt.Sprintf("- %v\n", e)
	}
	b += "\n## Acceptance Criteria\n- Updated definition passes validation and policy checks\n"
	b += "\n## Suggested Steps\n- Review state machine structure\n- Add required fields or transitions\n"
	b += "\nEffort: M\nOwner: TBD\n"
	if err := os.WriteFile(path, []byte(b), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write task %s: %v\n", path, err)
	}
}
