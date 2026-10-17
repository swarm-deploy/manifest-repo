package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"

	"github.com/bmatcuk/doublestar/v4"
)

// TemplateOptions controls optional Go template processing of a manifest.
type TemplateOptions struct {
	// Enabled executes the whole source as a strict Go template.
	Enabled bool
	// WorkspaceRoot is the root used by hashFiles.
	WorkspaceRoot string
	// Tag is available to templates as {{ .Tag }}.
	Tag string
}

// PrepareOptions controls template processing and repository label rendering.
type PrepareOptions struct {
	// Template configures optional template processing.
	Template TemplateOptions
	// Render configures repository label rendering.
	Render RenderOptions
}

// PrepareFile prepares a source manifest before merge or replace simulation.
func PrepareFile(source, output string, opts PrepareOptions) error {
	input := source
	if opts.Template.Enabled {
		templated, err := os.CreateTemp("", "manifest-repo-template-*.yaml")
		if err != nil {
			return fmt.Errorf("create templated manifest: %w", err)
		}
		input = templated.Name()
		if err := templated.Close(); err != nil {
			_ = os.Remove(input)
			return fmt.Errorf("close templated manifest: %w", err)
		}
		defer func() { _ = os.Remove(input) }()
		if err := ExecuteTemplateFile(source, input, opts.Template.WorkspaceRoot, opts.Template.Tag); err != nil {
			return err
		}
	}

	if opts.Render.SourceRepositoryURL != "" {
		return RenderFile(input, output, opts.Render)
	}

	doc, err := readDocument(input, false)
	if err != nil {
		return err
	}
	return writeDocument(output, doc)
}

// ExecuteTemplateFile executes a whole input file as a strict Go text/template.
func ExecuteTemplateFile(source, output, workspaceRoot, tag string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		workspaceRoot, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get workspace root: %w", err)
		}
	}

	funcs := template.FuncMap{
		"hashFiles": func(patterns ...string) (string, error) {
			return HashFiles(workspaceRoot, patterns...)
		},
	}
	tmpl, err := template.New(filepath.Base(source)).Option("missingkey=error").Funcs(funcs).Parse(string(data))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", source, err)
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", output, err)
	}
	file, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create %s: %w", output, err)
	}
	executeErr := tmpl.Execute(file, map[string]any{"Tag": tag})
	closeErr := file.Close()
	if executeErr != nil {
		return fmt.Errorf("execute template %s: %w", source, executeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", output, closeErr)
	}
	return nil
}

// HashFiles returns a GitHub Actions compatible hash for files below root.
func HashFiles(root string, patterns ...string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}

	matched := make(map[string]struct{})
	rootFS := os.DirFS(rootResolved)
	for _, original := range patterns {
		negative := strings.HasPrefix(original, "!")
		pattern := original
		if negative {
			pattern = strings.TrimPrefix(pattern, "!")
		}
		pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "/")
		if err := validateHashPattern(original, pattern); err != nil {
			return "", err
		}

		globOptions := []doublestar.GlobOption{doublestar.WithNoFollow(), doublestar.WithFailOnIOErrors()}
		if runtime.GOOS == "windows" {
			globOptions = append(globOptions, doublestar.WithCaseInsensitive())
		}
		matches, err := doublestar.Glob(rootFS, pattern, globOptions...)
		if err != nil {
			return "", fmt.Errorf("match hashFiles pattern %q: %w", original, err)
		}
		for _, match := range matches {
			clean := filepath.Clean(filepath.FromSlash(match))
			fullPath := filepath.Join(rootResolved, clean)
			resolved, err := filepath.EvalSymlinks(fullPath)
			if err != nil {
				return "", fmt.Errorf("resolve matched file %q: %w", match, err)
			}
			if !pathWithinRoot(rootResolved, resolved) {
				return "", fmt.Errorf("hashFiles match escapes workspace through symlink: %s", match)
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return "", fmt.Errorf("inspect matched file %q: %w", match, err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if negative {
				delete(matched, clean)
			} else {
				matched[clean] = struct{}{}
			}
		}
	}

	if len(matched) == 0 {
		return "", nil
	}
	files := make([]string, 0, len(matched))
	for file := range matched {
		files = append(files, file)
	}
	sort.Strings(files)

	combined := sha256.New()
	for _, file := range files {
		fileHash, err := hashFile(filepath.Join(rootResolved, file))
		if err != nil {
			return "", err
		}
		if _, err := combined.Write(fileHash); err != nil {
			return "", fmt.Errorf("combine hash for %s: %w", file, err)
		}
	}
	return hex.EncodeToString(combined.Sum(nil)), nil
}

func validateHashPattern(original, pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("hashFiles pattern %q is empty", original)
	}
	if filepath.IsAbs(filepath.FromSlash(pattern)) {
		return fmt.Errorf("hashFiles pattern escapes workspace: %s", original)
	}
	for _, part := range strings.Split(pattern, "/") {
		if part == ".." {
			return fmt.Errorf("hashFiles pattern escapes workspace: %s", original)
		}
	}
	if !doublestar.ValidatePathPattern(pattern) {
		return fmt.Errorf("invalid hashFiles pattern: %s", original)
	}
	return nil
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func hashFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open hashFiles match %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, fmt.Errorf("hash file %s: %w", path, err)
	}
	return hash.Sum(nil), nil
}
