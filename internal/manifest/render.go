package manifest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const GitHubRepositoryLabel = "org.swarm_deploy.github_repository"

type RenderOptions struct {
	StackName           string
	Registry            string
	ReleaseTag          string
	SourceRepositoryURL string
}

func RenderFile(source, output string, opts RenderOptions) error {
	if strings.TrimSpace(opts.StackName) == "" {
		return fmt.Errorf("stack name is required")
	}
	if strings.TrimSpace(opts.Registry) == "" {
		return fmt.Errorf("registry is required")
	}
	if strings.TrimSpace(opts.ReleaseTag) == "" {
		return fmt.Errorf("release tag is required")
	}
	if strings.TrimSpace(opts.SourceRepositoryURL) == "" {
		return fmt.Errorf("source repository URL is required")
	}

	doc, err := readDocument(source, false)
	if err != nil {
		return err
	}
	root := doc.Content[0]
	services, _, ok := mappingLookup(root, "services")
	if !ok || services.Kind != yaml.MappingNode {
		return fmt.Errorf("source compose section 'services' must be a mapping")
	}
	if len(services.Content) == 0 {
		return fmt.Errorf("source compose has no services to render")
	}

	registry := strings.TrimSuffix(opts.Registry, "/")
	for i := 0; i+1 < len(services.Content); i += 2 {
		serviceKey := services.Content[i].Value
		serviceCfg := services.Content[i+1]
		if serviceCfg.Kind != yaml.MappingNode {
			return fmt.Errorf("service %q must be a mapping", serviceKey)
		}

		image := fmt.Sprintf("%s/applications/%s/%s:%s", registry, opts.StackName, serviceKey, opts.ReleaseTag)
		mappingSet(serviceCfg, "image", scalarNode(image))

		deploy, _, ok := mappingLookup(serviceCfg, "deploy")
		if !ok || isNullNode(deploy) {
			deploy = mappingNode()
			mappingSet(serviceCfg, "deploy", deploy)
		}
		if deploy.Kind != yaml.MappingNode {
			return fmt.Errorf("service %q deploy must be a mapping", serviceKey)
		}

		if err := setRepositoryLabel(deploy, opts.SourceRepositoryURL, serviceKey); err != nil {
			return err
		}
	}

	return writeDocument(output, doc)
}

func setRepositoryLabel(deploy *yaml.Node, repositoryURL, serviceKey string) error {
	labels, _, ok := mappingLookup(deploy, "labels")
	if !ok || isNullNode(labels) {
		labels = mappingNode()
		mappingSet(labels, GitHubRepositoryLabel, scalarNode(repositoryURL))
		mappingSet(deploy, "labels", labels)
		return nil
	}

	switch labels.Kind {
	case yaml.MappingNode:
		mappingSet(labels, GitHubRepositoryLabel, scalarNode(repositoryURL))
		return nil
	case yaml.SequenceNode:
		prefix := GitHubRepositoryLabel + "="
		filtered := make([]*yaml.Node, 0, len(labels.Content)+1)
		for _, label := range labels.Content {
			if label.Kind != yaml.ScalarNode || label.Tag != "!!str" {
				return fmt.Errorf("service %q deploy labels list must contain strings", serviceKey)
			}
			if label.Value == GitHubRepositoryLabel || strings.HasPrefix(label.Value, prefix) {
				continue
			}
			filtered = append(filtered, label)
		}
		filtered = append(filtered, scalarNode(prefix+repositoryURL))
		labels.Content = filtered
		return nil
	default:
		return fmt.Errorf("service %q deploy labels must be a mapping or list", serviceKey)
	}
}
