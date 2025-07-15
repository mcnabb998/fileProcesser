# CRM File-Processor Profile v2 Audit

This report documents compliance against the provided validation matrix.

## Validation Results

| Category | Result | Notes |
|----------|--------|-------|
| JSON-Schema | **Fail** | `schema/profile_v2.schema.json` not found. |
| Unit Tests | **Fail** | `go test -cover` shows packages with 0% coverage. |
| Lint | **Pass** | `golangci-lint run` produced no issues. |
| Go Modules | **Fail** | `go mod tidy` modifies `go.mod` and `go.sum`. |
| README Completeness | **Fail** | Lambda READMEs missing required sections; `cmd/validator` lacks README. |
| Profile Files | **Fail** | No files under `/crm/file-profiles/`. |
| Cross-Refs | **Fail** | Cannot verify because profiles are missing. |
| Step-Function ASL | **Fail** | No `*.asl.json` or `*.asl.yaml` files found. |
| CI Workflow | **Fail** | `.github/workflows/ci.yml` lacks schema-lint step and full pipeline. |
| Docker Dev-Stack | **Fail** | `docker-compose.yml` absent. |


## Recommended Remediation Tasks
1. **Add canonical schema**: commit `schema/profile_v2.schema.json` matching the provided specification.
2. **Increase unit test coverage**: write tests for all packages to achieve 100% line and branch coverage.
3. **Pin go modules**: run `go mod tidy` and commit resulting files; ensure `go mod tidy --check` passes.
4. **Expand README files**: each lambda README should document purpose, I/O, environment variables, mermaid diagram, instructions for adding a new process, and a "Profile v2 Schema & Sample" section linking the schema and including example JSON. Add missing README for `cmd/validator`.
5. **Add profile definitions**: create JSON profiles under `crm/file-profiles/` and ensure they validate against the schema.
6. **Cross-reference validation**: confirm each profile's state machine ARN, parserId and targets exist in the codebase and ASL definitions.
7. **Provide ASL files**: store Step Function definitions in `*.asl.json` or `*.asl.yaml` format including `TimeoutSeconds`, `MaxConcurrency`, and proper `Retry`/`Catch` policies.
8. **Extend CI pipeline**: update `.github/workflows/ci.yml` to run lint → test → schema-lint → build → deploy in the correct order.
9. **Add Docker dev stack**: supply a `docker-compose.yml` with LocalStack and SAM local services for development.

