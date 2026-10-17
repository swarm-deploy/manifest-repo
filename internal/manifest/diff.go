package manifest

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/swarm-deploy/manifest-repo/internal/result"
	"gopkg.in/yaml.v3"
)

type logicalObject struct {
	id      string
	kind    string
	service string
	name    string
	fields  map[string]any
}

var sectionKinds = map[string]string{
	"services": "service",
	"configs":  "config",
	"secrets":  "secret",
	"networks": "network",
	"volumes":  "volume",
}

var safeDetailFields = map[string]struct{}{
	"image":       {},
	"replicas":    {},
	"ports":       {},
	"constraints": {},
}

// DiffFiles compares two manifests as logical Compose objects.
func DiffFiles(beforePath, afterPath string) ([]result.Change, error) {
	before, err := readDocument(beforePath, true)
	if err != nil {
		return nil, err
	}
	after, err := readDocument(afterPath, true)
	if err != nil {
		return nil, err
	}
	return DiffDocuments(before, after)
}

// DiffDocuments compares two parsed manifests as logical Compose objects.
func DiffDocuments(before, after *yaml.Node) ([]result.Change, error) {
	beforeObjects, err := logicalObjects(before)
	if err != nil {
		return nil, fmt.Errorf("read current desired state: %w", err)
	}
	afterObjects, err := logicalObjects(after)
	if err != nil {
		return nil, fmt.Errorf("read resulting desired state: %w", err)
	}

	ids := make(map[string]struct{}, len(beforeObjects)+len(afterObjects))
	for id := range beforeObjects {
		ids[id] = struct{}{}
	}
	for id := range afterObjects {
		ids[id] = struct{}{}
	}
	orderedIDs := make([]string, 0, len(ids))
	for id := range ids {
		orderedIDs = append(orderedIDs, id)
	}
	sort.Strings(orderedIDs)

	changes := make([]result.Change, 0)
	for _, id := range orderedIDs {
		beforeObject, hadBefore := beforeObjects[id]
		afterObject, hasAfter := afterObjects[id]
		if hadBefore && hasAfter && reflect.DeepEqual(beforeObject.fields, afterObject.fields) {
			continue
		}

		object := afterObject
		changeType := "updated"
		switch {
		case !hadBefore:
			changeType = "added"
		case !hasAfter:
			changeType = "removed"
			object = beforeObject
		}

		changedFields := fieldDiff(beforeObject.fields, afterObject.fields, hadBefore, hasAfter)
		change := result.Change{
			ID:            object.id,
			Kind:          object.kind,
			Service:       object.service,
			Name:          object.name,
			Change:        changeType,
			ChangedFields: changedFields,
		}
		for _, field := range changedFields {
			if _, safe := safeDetailFields[field]; !safe {
				continue
			}
			if change.Details == nil {
				change.Details = make(map[string]result.FieldChange)
			}
			detail := result.FieldChange{}
			if hadBefore {
				detail.Before = beforeObject.fields[field]
			}
			if hasAfter {
				detail.After = afterObject.fields[field]
			}
			change.Details[field] = detail
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func logicalObjects(doc *yaml.Node) (map[string]logicalObject, error) {
	objects := make(map[string]logicalObject)
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("invalid YAML document")
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		name := root.Content[i].Value
		if _, isSection := sectionKinds[name]; isSection {
			continue
		}
		var value any
		if err := root.Content[i+1].Decode(&value); err != nil {
			return nil, fmt.Errorf("top-level field %q: %w", name, err)
		}
		id := "top-level/" + name
		objects[id] = logicalObject{id: id, kind: "top_level", name: name, fields: map[string]any{"value": value}}
	}
	for section, kind := range sectionKinds {
		sectionNode, _, ok := mappingLookup(root, section)
		if !ok || isNullNode(sectionNode) {
			continue
		}
		if sectionNode.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("section %s must be a mapping", section)
		}
		for i := 0; i+1 < len(sectionNode.Content); i += 2 {
			name := sectionNode.Content[i].Value
			value := sectionNode.Content[i+1]
			fields, err := nodeFields(value, kind != "service")
			if err != nil {
				return nil, fmt.Errorf("%s %q: %w", kind, name, err)
			}

			if kind == "service" {
				jobs, hasJobs := fields["x-init-deploy-jobs"]
				delete(fields, "x-init-deploy-jobs")
				flattenSafeDeployFields(fields)
				if hasJobs {
					if err := addInitJobs(objects, name, jobs); err != nil {
						return nil, err
					}
				}
			}

			id := kind + "/" + name
			objects[id] = logicalObject{id: id, kind: kind, name: name, fields: fields}
		}
	}
	return objects, nil
}

func addInitJobs(objects map[string]logicalObject, service string, raw any) error {
	if raw == nil {
		return nil
	}
	jobs, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("service %q x-init-deploy-jobs must be a list", service)
	}
	for index, rawJob := range jobs {
		fields, ok := rawJob.(map[string]any)
		if !ok {
			return fmt.Errorf("service %q init job %d must be a mapping", service, index)
		}
		name, ok := fields["name"].(string)
		if !ok || name == "" {
			return fmt.Errorf("service %q init job %d must have a name", service, index)
		}
		fields = cloneMap(fields)
		delete(fields, "name")
		id := "init-job/" + service + "/" + name
		if _, exists := objects[id]; exists {
			return fmt.Errorf("service %q has duplicate init job %q", service, name)
		}
		objects[id] = logicalObject{id: id, kind: "init_job", service: service, name: name, fields: fields}
	}
	return nil
}

func nodeFields(node *yaml.Node, allowNull bool) (map[string]any, error) {
	if allowNull && isNullNode(node) {
		return make(map[string]any), nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("definition must be a mapping")
	}
	var fields map[string]any
	if err := node.Decode(&fields); err != nil {
		return nil, err
	}
	if fields == nil {
		fields = make(map[string]any)
	}
	return fields, nil
}

func flattenSafeDeployFields(fields map[string]any) {
	deploy, ok := fields["deploy"].(map[string]any)
	if !ok {
		return
	}
	deploy = cloneMap(deploy)
	if replicas, ok := deploy["replicas"]; ok {
		fields["replicas"] = replicas
		delete(deploy, "replicas")
	}
	if placement, ok := deploy["placement"].(map[string]any); ok {
		placement = cloneMap(placement)
		if constraints, ok := placement["constraints"]; ok {
			fields["constraints"] = constraints
			delete(placement, "constraints")
		}
		if len(placement) == 0 {
			delete(deploy, "placement")
		} else {
			deploy["placement"] = placement
		}
	}
	if len(deploy) == 0 {
		delete(fields, "deploy")
	} else {
		fields["deploy"] = deploy
	}
}

func fieldDiff(before, after map[string]any, hadBefore, hasAfter bool) []string {
	fields := make(map[string]struct{}, len(before)+len(after))
	if hadBefore {
		for field := range before {
			fields[field] = struct{}{}
		}
	}
	if hasAfter {
		for field := range after {
			fields[field] = struct{}{}
		}
	}
	changed := make([]string, 0, len(fields))
	for field := range fields {
		if !hadBefore || !hasAfter || !reflect.DeepEqual(before[field], after[field]) {
			changed = append(changed, field)
		}
	}
	sort.Strings(changed)
	return changed
}

func cloneMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
