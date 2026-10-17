package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	githuboutput "github.com/ci-space/github-output"
	"github.com/swarm-deploy/manifest-repo/internal/manifest"
	"github.com/swarm-deploy/manifest-repo/internal/publisher"
	"github.com/swarm-deploy/manifest-repo/internal/result"
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
		return runOperation(args[1:], false)
	case "merge":
		return runMerge(args[1:])
	case "publish":
		return runOperation(args[1:], true)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runOperation(args []string, publish bool) error {
	name := "render"
	if publish {
		name = "publish"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	source := fs.String("source", "deploy/prod.yaml", "source Compose manifest")
	output := fs.String("output", "", "resulting manifest path")
	stack := fs.String("stack", "", "stack name")
	tag := fs.String("tag", "", "optional Go template variable {{ .Tag }}")
	repositoryURL := fs.String("source-repository-url", "", "source repository URL added to service labels and provenance")
	sourceRevision := fs.String("source-revision", "", "source repository revision for provenance")
	repository := fs.String("repo", "", "target repository as owner/name, URL, or local path")
	branch := fs.String("branch", "main", "target repository branch")
	target := fs.String("target", "", "target path inside repository")
	mode := fs.String("mode", string(manifest.MergeModeMerge), "operation mode: merge or replace")
	tokenEnv := fs.String("token-env", "MANIFEST_REPO_TOKEN", "environment variable containing Git token")
	gitUser := fs.String("git-user", "manifest-repo", "Git commit author name")
	gitEmail := fs.String("git-email", "manifest-repo@users.noreply.github.com", "Git commit author email")
	message := fs.String("message", "", "Git commit message")
	validate := fs.Bool("validate-compose", true, "validate target with docker compose config")
	templateInput := fs.Bool("template", false, "execute the whole input as a strict Go template")
	workspace := fs.String("workspace", defaultWorkspace(), "workspace root used by hashFiles")
	rendered := fs.Bool("rendered", false, "source is a previously rendered manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" && *stack != "" {
		if !validStackName(*stack) {
			return fmt.Errorf("invalid stack name %q: allowed characters are letters, digits, dot, underscore, and hyphen", *stack)
		}
		*target = filepath.ToSlash(filepath.Join("applications", *stack+".yaml"))
	}
	if !publish && *output == "" {
		return fmt.Errorf("--output is required for render")
	}

	cfg := publisher.Config{
		SourceFile:       *source,
		OutputFile:       *output,
		SourceIsRendered: *rendered,
		Template:         *templateInput,
		Tag:              *tag,
		WorkspaceRoot:    *workspace,
		RenderOptions: manifest.RenderOptions{
			SourceRepositoryURL: *repositoryURL,
		},
		Repository:       *repository,
		Branch:           *branch,
		TargetFile:       *target,
		Stack:            *stack,
		Mode:             manifest.MergeMode(*mode),
		Token:            os.Getenv(*tokenEnv),
		GitUserName:      *gitUser,
		GitUserEmail:     *gitEmail,
		CommitMessage:    *message,
		ValidateCompose:  *validate,
		SourceRepository: *repositoryURL,
		SourceRevision:   *sourceRevision,
	}

	var resultValue result.Result
	var err error
	if publish {
		resultValue, err = publisher.Publish(cfg)
	} else {
		resultValue, err = publisher.Render(cfg)
	}
	if err != nil {
		return err
	}
	return emitResult(resultValue, *output)
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

func emitResult(resultValue result.Result, output string) error {
	compact, err := json.Marshal(resultValue)
	if err != nil {
		return fmt.Errorf("encode structured result: %w", err)
	}
	pretty, err := json.MarshalIndent(resultValue, "", "  ")
	if err != nil {
		return fmt.Errorf("encode structured result: %w", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, string(pretty)); err != nil {
		return fmt.Errorf("write structured result: %w", err)
	}

	revision := ""
	if resultValue.DesiredState != nil {
		revision = resultValue.DesiredState.Revision
	}
	return githuboutput.WhenAvailable(func() error {
		return githuboutput.WriteMap(map[string]string{
			"result":            string(compact),
			"changed":           fmt.Sprintf("%t", resultValue.Changed),
			"rendered-manifest": output,
			"revision":          revision,
		})
	})
}

func defaultWorkspace() string {
	if workspace := os.Getenv("GITHUB_WORKSPACE"); workspace != "" {
		return workspace
	}
	return "."
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
  render   simulate an update and write the resulting manifest plus semantic JSON
  merge    merge or replace a local manifest file
  publish  run the render pipeline, validate, commit, and push`)
}
