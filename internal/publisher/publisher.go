package publisher

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/swarm-deploy/manifest-repo/internal/manifest"
	"github.com/swarm-deploy/manifest-repo/internal/result"
)

// Config describes a render or publish operation.
type Config struct {
	// SourceFile is the application manifest or a previously rendered manifest.
	SourceFile string
	// OutputFile receives the resulting manifest when set.
	OutputFile string
	// SourceIsRendered skips template and repository label rendering when true.
	SourceIsRendered bool
	// Template enables strict Go template processing before YAML parsing.
	Template bool
	// WorkspaceRoot is the source repository root used by hashFiles.
	WorkspaceRoot string
	// Tag is available to Go templates as {{ .Tag }}.
	Tag string
	// RenderOptions configure repository label rendering.
	RenderOptions manifest.RenderOptions
	// Repository is the desired-state Git repository.
	Repository string
	// Branch is the desired-state repository branch.
	Branch string
	// TargetFile is the manifest path inside the desired-state repository.
	TargetFile string
	// Stack is the logical stack name included in structured results.
	Stack string
	// Mode selects merge or replace simulation.
	Mode manifest.MergeMode
	// Token authenticates HTTP Git operations.
	Token string
	// GitUserName is the publish commit author name.
	GitUserName string
	// GitUserEmail is the publish commit author email.
	GitUserEmail string
	// CommitMessage is the publish commit message.
	CommitMessage string
	// ValidateCompose enables docker compose config validation.
	ValidateCompose bool
	// SourceRepository identifies source provenance when available.
	SourceRepository string
	// SourceRevision identifies source provenance when available.
	SourceRevision string
}

// Render simulates an update against the current desired state without pushing.
func Render(cfg Config) (result.Result, error) {
	return execute(cfg, false)
}

// Publish simulates, validates, commits, and pushes a desired-state update.
func Publish(cfg Config) (result.Result, error) {
	return execute(cfg, true)
}

func execute(cfg Config, publish bool) (result.Result, error) {
	operation := "render"
	if publish {
		operation = "publish"
	}
	resultValue := result.Result{
		SchemaVersion: "v1",
		Operation:     operation,
		Stack:         cfg.Stack,
		Target:        filepath.ToSlash(cfg.TargetFile),
		Changes:       []result.Change{},
	}
	if err := validateConfig(cfg, publish); err != nil {
		return resultValue, err
	}
	if cfg.GitUserName == "" {
		cfg.GitUserName = "manifest-repo"
	}
	if cfg.GitUserEmail == "" {
		cfg.GitUserEmail = "manifest-repo@users.noreply.github.com"
	}
	if cfg.SourceRepository != "" || cfg.SourceRevision != "" {
		resultValue.Source = &result.Provenance{Repository: provenanceRepository(cfg.SourceRepository), Revision: cfg.SourceRevision}
	}

	sourcePath, err := filepath.Abs(cfg.SourceFile)
	if err != nil {
		return resultValue, fmt.Errorf("resolve source file: %w", err)
	}
	outputPath := ""
	if cfg.OutputFile != "" {
		outputPath, err = filepath.Abs(cfg.OutputFile)
		if err != nil {
			return resultValue, fmt.Errorf("resolve output file: %w", err)
		}
	}
	workspaceRoot := cfg.WorkspaceRoot
	if workspaceRoot == "" {
		workspaceRoot, err = os.Getwd()
		if err != nil {
			return resultValue, fmt.Errorf("get workspace root: %w", err)
		}
	}

	workDir, err := os.MkdirTemp("", "manifest-repo-"+operation+"-*")
	if err != nil {
		return resultValue, fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	repoDir := filepath.Join(workDir, "repo")
	repoURL := repositoryURL(cfg.Repository)
	authEnv := gitAuthEnv(cfg.Token, repoURL)
	if err := runGit("", authEnv, "clone", "--depth", "1", "--branch", cfg.Branch, repoURL, repoDir); err != nil {
		return resultValue, err
	}
	beforeRevision, err := outputCommand(repoDir, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return resultValue, fmt.Errorf("read desired-state revision: %w", err)
	}
	resultValue.DesiredState = &result.Provenance{Repository: provenanceRepository(cfg.Repository), Revision: strings.TrimSpace(beforeRevision)}

	preparedSource := sourcePath
	prepareSource := cfg.Template || cfg.RenderOptions.SourceRepositoryURL != ""
	if !cfg.SourceIsRendered && prepareSource {
		preparedSource = filepath.Join(workDir, "prepared.yaml")
		if err := manifest.PrepareFile(sourcePath, preparedSource, manifest.PrepareOptions{
			Template: manifest.TemplateOptions{Enabled: cfg.Template, WorkspaceRoot: workspaceRoot, Tag: cfg.Tag},
			Render:   cfg.RenderOptions,
		}); err != nil {
			return resultValue, fmt.Errorf("prepare manifest: %w", err)
		}
	}

	targetPath, err := safeTargetPath(repoDir, cfg.TargetFile)
	if err != nil {
		return resultValue, err
	}
	currentPath := filepath.Join(workDir, "current.yaml")
	if err := copyIfExists(targetPath, currentPath); err != nil {
		return resultValue, err
	}
	if err := manifest.ApplyFile(preparedSource, targetPath, cfg.Mode); err != nil {
		return resultValue, err
	}

	changes, err := manifest.DiffFiles(currentPath, targetPath)
	if err != nil {
		return resultValue, err
	}
	resultValue.Changes = changes
	resultValue.Changed = len(changes) > 0

	if outputPath != "" {
		if err := copyFile(targetPath, outputPath); err != nil {
			return resultValue, fmt.Errorf("write resulting manifest: %w", err)
		}
	}
	if publish && cfg.ValidateCompose {
		if err := runCommand(repoDir, nil, "docker", "compose", "-f", cfg.TargetFile, "config"); err != nil {
			return resultValue, fmt.Errorf("validate compose: %w", err)
		}
	}
	if !publish || !resultValue.Changed {
		return resultValue, nil
	}

	if err := runGit(repoDir, nil, "config", "user.name", cfg.GitUserName); err != nil {
		return resultValue, err
	}
	if err := runGit(repoDir, nil, "config", "user.email", cfg.GitUserEmail); err != nil {
		return resultValue, err
	}
	if err := runGit(repoDir, nil, "add", "--", cfg.TargetFile); err != nil {
		return resultValue, err
	}
	if err := runGit(repoDir, nil, "commit", "-m", cfg.CommitMessage); err != nil {
		return resultValue, err
	}
	if err := runGit(repoDir, authEnv, "push", "origin", cfg.Branch); err != nil {
		return resultValue, err
	}
	afterRevision, err := outputCommand(repoDir, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return resultValue, fmt.Errorf("read published desired-state revision: %w", err)
	}
	resultValue.DesiredState.Revision = strings.TrimSpace(afterRevision)
	return resultValue, nil
}

func validateConfig(cfg Config, publish bool) error {
	if cfg.SourceFile == "" {
		return fmt.Errorf("source file is required")
	}
	if cfg.Repository == "" {
		return fmt.Errorf("repository is required")
	}
	if cfg.Branch == "" {
		return fmt.Errorf("branch is required")
	}
	if cfg.TargetFile == "" {
		return fmt.Errorf("target file is required")
	}
	if publish && cfg.CommitMessage == "" {
		return fmt.Errorf("commit message is required")
	}
	return nil
}

func repositoryURL(repository string) string {
	if strings.Contains(repository, "://") || strings.HasPrefix(repository, "git@") || strings.HasPrefix(repository, "/") || strings.HasPrefix(repository, "./") || strings.HasPrefix(repository, "../") {
		return repository
	}
	return "https://github.com/" + strings.TrimSuffix(repository, ".git") + ".git"
}

func provenanceRepository(repository string) string {
	parsed, err := url.Parse(repository)
	if err != nil || parsed.Scheme == "" {
		return repository
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func gitAuthEnv(token, repository string) []string {
	if token == "" {
		return nil
	}

	repositoryURL, err := url.Parse(repository)
	if err != nil || repositoryURL.Host == "" || (repositoryURL.Scheme != "http" && repositoryURL.Scheme != "https") {
		return nil
	}
	repositoryURL.RawQuery = ""
	repositoryURL.Fragment = ""
	authScope := repositoryURL.String()

	credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http." + authScope + ".extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + credentials,
	}
}

func safeTargetPath(repoDir, target string) (string, error) {
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("target file must be relative to the repository")
	}
	clean := filepath.Clean(filepath.FromSlash(target))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target file escapes the repository: %s", target)
	}
	return filepath.Join(repoDir, clean), nil
}

func copyIfExists(source, destination string) error {
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect current desired state: %w", err)
	}
	return copyFile(source, destination)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer func() { _ = input.Close() }()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", destination, err)
	}
	output, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy %s to %s: %w", source, destination, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close %s: %w", destination, err)
	}
	return nil
}

func runGit(dir string, extraEnv []string, args ...string) error {
	if err := runCommand(dir, extraEnv, "git", args...); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func runCommand(dir string, extraEnv []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func outputCommand(dir string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), exitErr)
		}
		return "", err
	}
	return string(output), nil
}
