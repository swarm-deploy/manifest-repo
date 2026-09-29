package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swarm-deploy/manifest-repo/internal/manifest"
	"github.com/swarm-deploy/manifest-repo/internal/publisher"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return flag.ErrHelp
	}

	switch args[0] {
	case "render":
		return runRender(args[1:])
	case "merge":
		return runMerge(args[1:])
	case "publish":
		return runPublish(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	source := fs.String("source", "deploy/prod.yaml", "source Compose manifest")
	output := fs.String("output", "", "rendered manifest path")
	stack := fs.String("stack", "", "stack name")
	registry := fs.String("registry", "", "container registry")
	tag := fs.String("tag", "", "release image tag")
	repositoryURL := fs.String("source-repository-url", "", "source repository URL added to service labels")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("--output is required")
	}
	return manifest.RenderFile(*source, *output, manifest.RenderOptions{
		StackName:           *stack,
		Registry:            *registry,
		ReleaseTag:          *tag,
		SourceRepositoryURL: *repositoryURL,
	})
}

func runMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	source := fs.String("source", "", "source manifest")
	target := fs.String("target", "", "target manifest")
	mode := fs.String("mode", string(manifest.MergeModeMerge), "merge mode: merge or replace")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" {
		return fmt.Errorf("--source is required")
	}
	if *target == "" {
		return fmt.Errorf("--target is required")
	}
	return manifest.ApplyFile(*source, *target, manifest.MergeMode(*mode))
}

func runPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	source := fs.String("source", "", "source manifest")
	registry := fs.String("registry", "", "container registry used to render service images")
	tag := fs.String("tag", "", "release image tag used to render service images")
	repositoryURL := fs.String("source-repository-url", "", "source repository URL added to service labels")
	repository := fs.String("repo", "", "target repository as owner/name, URL, or local path")
	branch := fs.String("branch", "main", "target repository branch")
	target := fs.String("target", "", "target path inside repository")
	stack := fs.String("stack", "", "stack name; defaults target to applications/<stack>.yaml")
	mode := fs.String("mode", string(manifest.MergeModeMerge), "publish mode: merge or replace")
	tokenEnv := fs.String("token-env", "MANIFEST_REPO_TOKEN", "environment variable containing Git token")
	gitUser := fs.String("git-user", "manifest-repo", "Git commit author name")
	gitEmail := fs.String("git-email", "manifest-repo@users.noreply.github.com", "Git commit author email")
	message := fs.String("message", "", "Git commit message")
	validate := fs.Bool("validate-compose", true, "validate target with docker compose config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" && *stack != "" {
		if !validStackName(*stack) {
			return fmt.Errorf("invalid stack name %q: allowed characters are letters, digits, dot, underscore, and hyphen", *stack)
		}
		*target = filepath.ToSlash(filepath.Join("applications", *stack+".yaml"))
	}
	if *message == "" {
		return fmt.Errorf("--message is required")
	}

	publishSource := *source
	if *registry != "" || *tag != "" || *repositoryURL != "" {
		rendered, err := os.CreateTemp("", "manifest-repo-rendered-*.yaml")
		if err != nil {
			return fmt.Errorf("create rendered manifest: %w", err)
		}
		publishSource = rendered.Name()
		if err := rendered.Close(); err != nil {
			_ = os.Remove(publishSource)
			return fmt.Errorf("close rendered manifest: %w", err)
		}
		defer func() { _ = os.Remove(publishSource) }()

		if err := manifest.RenderFile(*source, publishSource, manifest.RenderOptions{
			StackName:           *stack,
			Registry:            *registry,
			ReleaseTag:          *tag,
			SourceRepositoryURL: *repositoryURL,
		}); err != nil {
			return fmt.Errorf("render manifest: %w", err)
		}
	}

	changed, err := publisher.Publish(publisher.Config{
		SourceFile:      publishSource,
		Repository:      *repository,
		Branch:          *branch,
		TargetFile:      *target,
		Mode:            manifest.MergeMode(*mode),
		Token:           os.Getenv(*tokenEnv),
		GitUserName:     *gitUser,
		GitUserEmail:    *gitEmail,
		CommitMessage:   *message,
		ValidateCompose: *validate,
	})
	if err != nil {
		return err
	}
	if changed {
		fmt.Println("manifest published")
	} else {
		fmt.Println("no manifest changes")
	}
	return nil
}

func validStackName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `manifest-repo manages manifests published from application repositories into a central GitOps repository.

Usage:
  manifest-repo render  [flags]
  manifest-repo merge   [flags]
  manifest-repo publish [flags]

Commands:
  render   rewrite release images and add source repository labels
  merge    merge or replace a manifest file
  publish  update a manifest in a Git repository, commit, and push it`)
}
