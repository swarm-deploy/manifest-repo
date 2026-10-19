package manifest

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const GitHubRepositoryLabel = "org.swarm_deploy.github_repository"

type RenderOptions struct {
	// SourceRepositoryURL is written to the swarm-deploy repository label.
	SourceRepositoryURL string
	// GlobalEnvFiles are repository-root-relative env_file paths added to every service.
	GlobalEnvFiles []string
	// TargetFile is the repository-relative Compose file path used to make GlobalEnvFiles relative.
	TargetFile string
}

func RenderFile(source, output string, opts RenderOptions) error {
	globalEnvFiles, err := targetRelativeEnvFiles(opts.GlobalEnvFiles, opts.TargetFile)
	if err != nil {
		return err
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

	for i := 0; i+1 < len(services.Content); i += 2 {
		serviceKey := services.Content[i].Value
		serviceCfg := services.Content[i+1]
		if serviceCfg.Kind != yaml.MappingNode {
			return fmt.Errorf("service %q must be a mapping", serviceKey)
		}

		if len(globalEnvFiles) > 0 {
			if err := prependEnvFiles(serviceCfg, globalEnvFiles, serviceKey); err != nil {
				return err
			}
		}

		if strings.TrimSpace(opts.SourceRepositoryURL) != "" {
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
	}

	return writeDocument(output, doc)
}

func targetRelativeEnvFiles(files []string, targetFile string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(targetFile) == "" {
		return nil, fmt.Errorf("target file is required when global env files are configured")
	}

	targetDir := filepath.Dir(filepath.Clean(filepath.FromSlash(targetFile)))
	seen := make(map[string]struct{}, len(files))
	result := make([]string, 0, len(files))
	for _, file := range files {
		clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(file)))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("global env file must be relative to the target repository root: %q", file)
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}

		relative, err := filepath.Rel(targetDir, clean)
		if err != nil {
			return nil, fmt.Errorf("resolve global env file %q relative to target %q: %w", file, targetFile, err)
		}
		result = append(result, filepath.ToSlash(relative))
	}
	return result, nil
}

func prependEnvFiles(service *yaml.Node, globalFiles []string, serviceKey string) error {
	envFile, _, ok := mappingLookup(service, "env_file")
	existingPaths := make(map[string]struct{})
	if ok && !isNullNode(envFile) {
		switch envFile.Kind {
		case yaml.ScalarNode:
			if envFile.Tag != "!!str" {
				return fmt.Errorf("service %q env_file must be a string or list", serviceKey)
			}
			existingPaths[filepath.Clean(filepath.FromSlash(envFile.Value))] = struct{}{}
		case yaml.SequenceNode:
			for _, entry := range envFile.Content {
				if path, ok := envFileEntryPath(entry); ok {
					existingPaths[filepath.Clean(filepath.FromSlash(path))] = struct{}{}
				}
			}
		default:
			return fmt.Errorf("service %q env_file must be a string or list", serviceKey)
		}
	}

	content := make([]*yaml.Node, 0, len(globalFiles)+1)
	for _, file := range globalFiles {
		clean := filepath.Clean(filepath.FromSlash(file))
		if _, exists := existingPaths[clean]; exists {
			continue
		}
		content = append(content, scalarNode(file))
	}
	if ok && !isNullNode(envFile) {
		if envFile.Kind == yaml.SequenceNode {
			content = append(content, envFile.Content...)
		} else {
			content = append(content, envFile)
		}
	}

	if len(content) == 0 {
		return nil
	}
	mappingSet(service, "env_file", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: content})
	return nil
}

func envFileEntryPath(entry *yaml.Node) (string, bool) {
	if entry.Kind == yaml.ScalarNode && entry.Tag == "!!str" {
		return entry.Value, true
	}
	if entry.Kind != yaml.MappingNode {
		return "", false
	}
	path, _, ok := mappingLookup(entry, "path")
	if !ok || path.Kind != yaml.ScalarNode || path.Tag != "!!str" {
		return "", false
	}
	return path.Value, true
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
