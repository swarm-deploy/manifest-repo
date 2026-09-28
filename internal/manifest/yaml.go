package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func readDocument(path string, allowMissing bool) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if allowMissing && os.IsNotExist(err) {
			return newDocument(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return newDocument(), nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: root must be a mapping", path)
	}
	return &doc, nil
}

func writeDocument(path string, doc *yaml.Node) error {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return fmt.Errorf("invalid YAML document")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc.Content[0]); err != nil {
		_ = enc.Close()
		return fmt.Errorf("encode YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close YAML encoder: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func newDocument() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
		}},
	}
}

func mappingLookup(mapping *yaml.Node, key string) (*yaml.Node, int, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, -1, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], i, true
		}
	}
	return nil, -1, false
}

func mappingSet(mapping *yaml.Node, key string, value *yaml.Node) {
	if _, idx, ok := mappingLookup(mapping, key); ok {
		mapping.Content[idx+1] = value
		return
	}
	mapping.Content = append(mapping.Content, scalarNode(key), value)
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func mappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	copy := *n
	copy.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		copy.Content[i] = cloneNode(child)
	}
	return &copy
}
