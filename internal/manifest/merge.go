package manifest

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type MergeMode string

const (
	MergeModeMerge   MergeMode = "merge"
	MergeModeReplace MergeMode = "replace"
)

var mergeSections = map[string]struct{}{
	"services": {},
	"networks": {},
	"volumes":  {},
	"secrets":  {},
	"configs":  {},
}

func ApplyFile(source, target string, mode MergeMode) error {
	switch mode {
	case MergeModeReplace:
		return replaceFile(source, target)
	case MergeModeMerge:
		return mergeFile(source, target)
	default:
		return fmt.Errorf("unsupported mode %q: allowed values are %q and %q", mode, MergeModeReplace, MergeModeMerge)
	}
}

func replaceFile(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", target, err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}

func mergeFile(source, target string) error {
	sourceDoc, err := readDocument(source, false)
	if err != nil {
		return err
	}
	targetDoc, err := readDocument(target, true)
	if err != nil {
		return err
	}

	sourceRoot := sourceDoc.Content[0]
	targetRoot := targetDoc.Content[0]
	for i := 0; i+1 < len(sourceRoot.Content); i += 2 {
		key := sourceRoot.Content[i].Value
		sourceValue := sourceRoot.Content[i+1]

		if _, mergeSection := mergeSections[key]; !mergeSection {
			mappingSet(targetRoot, key, cloneNode(sourceValue))
			continue
		}

		if sourceValue.Kind != yaml.MappingNode {
			return fmt.Errorf("source section %s must be a mapping", key)
		}

		targetValue, _, ok := mappingLookup(targetRoot, key)
		if !ok || isNullNode(targetValue) {
			targetValue = mappingNode()
			mappingSet(targetRoot, key, targetValue)
		}
		if targetValue.Kind != yaml.MappingNode {
			return fmt.Errorf("target section %s must be a mapping", key)
		}

		for j := 0; j+1 < len(sourceValue.Content); j += 2 {
			mappingSet(targetValue, sourceValue.Content[j].Value, cloneNode(sourceValue.Content[j+1]))
		}
	}

	return writeDocument(target, targetDoc)
}

func isNullNode(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}
