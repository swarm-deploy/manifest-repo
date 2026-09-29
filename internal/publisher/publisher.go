package publisher

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/swarm-deploy/manifest-repo/internal/manifest"
)

type Config struct {
	SourceFile      string
	Repository      string
	Branch          string
	TargetFile      string
	Mode            manifest.MergeMode
	Token           string
	GitUserName     string
	GitUserEmail    string
	CommitMessage   string
	ValidateCompose bool
}

func Publish(cfg Config) (bool, error) {
	if cfg.SourceFile == "" {
		return false, fmt.Errorf("source file is required")
	}
	if cfg.Repository == "" {
		return false, fmt.Errorf("repository is required")
	}
	if cfg.Branch == "" {
		return false, fmt.Errorf("branch is required")
	}
	if cfg.TargetFile == "" {
		return false, fmt.Errorf("target file is required")
	}
	if cfg.CommitMessage == "" {
		return false, fmt.Errorf("commit message is required")
	}
	if cfg.GitUserName == "" {
		cfg.GitUserName = "manifest-repo"
	}
	if cfg.GitUserEmail == "" {
		cfg.GitUserEmail = "manifest-repo@users.noreply.github.com"
	}

	workDir, err := os.MkdirTemp("", "manifest-repo-publish-*")
	if err != nil {
		return false, fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	repoDir := filepath.Join(workDir, "repo")
	repoURL := repositoryURL(cfg.Repository)
	authEnv := gitAuthEnv(cfg.Token)
	if err := runGit("", authEnv, "clone", "--depth", "1", "--branch", cfg.Branch, repoURL, repoDir); err != nil {
		return false, err
	}

	targetPath, err := safeTargetPath(repoDir, cfg.TargetFile)
	if err != nil {
		return false, err
	}
	if err := manifest.ApplyFile(cfg.SourceFile, targetPath, cfg.Mode); err != nil {
		return false, err
	}

	if cfg.ValidateCompose {
		if err := runCommand(repoDir, nil, "docker", "compose", "-f", cfg.TargetFile, "config"); err != nil {
			return false, fmt.Errorf("validate compose: %w", err)
		}
	}

	status, err := outputCommand(repoDir, nil, "git", "status", "--porcelain", "--", cfg.TargetFile)
	if err != nil {
		return false, fmt.Errorf("check git status: %w", err)
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}

	if err := runGit(repoDir, nil, "config", "user.name", cfg.GitUserName); err != nil {
		return false, err
	}
	if err := runGit(repoDir, nil, "config", "user.email", cfg.GitUserEmail); err != nil {
		return false, err
	}
	if err := runGit(repoDir, nil, "add", "--", cfg.TargetFile); err != nil {
		return false, err
	}
	if err := runGit(repoDir, nil, "commit", "-m", cfg.CommitMessage); err != nil {
		return false, err
	}
	if err := runGit(repoDir, authEnv, "push", "origin", cfg.Branch); err != nil {
		return false, err
	}

	return true, nil
}

func repositoryURL(repository string) string {
	if strings.Contains(repository, "://") || strings.HasPrefix(repository, "git@") || strings.HasPrefix(repository, "/") || strings.HasPrefix(repository, "./") || strings.HasPrefix(repository, "../") {
		return repository
	}
	return "https://github.com/" + strings.TrimSuffix(repository, ".git") + ".git"
}

func gitAuthEnv(token string) []string {
	if token == "" {
		return nil
	}
	credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
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
