package validator

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"

	_ "embed"
)

//go:embed aws-states-language.json
var schemaData []byte
var schema *jsonschema.Schema

func init() {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", strings.NewReader(string(schemaData))); err != nil {
		panic(err)
	}
	var err error
	schema, err = compiler.Compile("schema.json")
	if err != nil {
		panic(err)
	}
}

// ASLFile holds info about a Step Function definition.
type ASLFile struct {
	Path   string
	Errors []error
}

// DiscoverASL finds .asl.json and .asl.yaml files under the given roots.
func DiscoverASL(roots []string) ([]string, error) {
	var files []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".asl.json") || strings.HasSuffix(path, ".asl.yaml") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return files, err
		}
	}
	return files, nil
}

// ValidateASL validates the given Step Function file.
func ValidateASL(path string) []error {
	data, err := os.ReadFile(path)
	if err != nil {
		return []error{fmt.Errorf("read: %w", err)}
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return []error{fmt.Errorf("decode: %w", err)}
	}
	if err := schema.Validate(v); err != nil {
		if ve, ok := err.(*jsonschema.ValidationError); ok {
			var errs []error
			for _, e := range ve.Causes {
				errs = append(errs, fmt.Errorf(e.InstanceLocation+": "+e.Message))
			}
			if len(errs) == 0 {
				errs = append(errs, err)
			}
			return errs
		}
		return []error{err}
	}
	return nil
}
