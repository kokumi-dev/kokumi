package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/afero"
)

// listFiles walks dir recursively and returns all .yaml/.yml/.json files with
// their paths relative to dir, sorted by path. This is the richer superset
// semantics previously implemented (divergently) in the server.
func listFiles(fs afero.Fs, dir string) ([]File, error) {
	var files []File

	err := afero.Walk(fs, dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}

		data, err := afero.ReadFile(fs, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = filepath.Base(path)
		}

		files = append(files, File{Path: filepath.ToSlash(rel), Content: string(data)})
		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })

	return files, nil
}

// joinFiles concatenates file contents with "---" separators.
func joinFiles(files []File) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, f.Content)
	}

	return strings.Join(parts, "\n---\n")
}

// mergeYAMLFiles merges all top-level YAML files in dir into a single
// manifest.yaml. Kustomization files are excluded from the merge.
func mergeYAMLFiles(fs afero.Fs, dir string) error {
	files, err := listTopLevelYAML(fs, dir)
	if err != nil {
		return err
	}

	var keep []string
	for _, file := range files {
		switch filepath.Base(file) {
		case "kustomization.yaml", "kustomization.yml", "Kustomization":
			continue
		}
		keep = append(keep, file)
	}

	if len(keep) == 0 {
		return nil
	}

	if len(keep) == 1 && filepath.Base(keep[0]) == "manifest.yaml" {
		return nil
	}

	var renderedManifest strings.Builder
	for _, file := range keep {
		content, err := afero.ReadFile(fs, file)
		if err != nil {
			return fmt.Errorf("read %q: %w", file, err)
		}

		renderedManifest.WriteString("---\n")
		fmt.Fprintf(&renderedManifest, "# Source: %s\n", filepath.Base(file))
		renderedManifest.WriteString(strings.TrimSpace(string(content)))
		renderedManifest.WriteString("\n")
	}

	for _, file := range keep {
		if err := fs.Remove(file); err != nil {
			return fmt.Errorf("remove %q: %w", file, err)
		}
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "manifest.yaml"), []byte(renderedManifest.String()), 0600); err != nil {
		return fmt.Errorf("write manifest.yaml: %w", err)
	}

	return nil
}

// listTopLevelYAML returns the sorted top-level YAML file paths in dir.
func listTopLevelYAML(fs afero.Fs, dir string) ([]string, error) {
	var files []string
	for _, pattern := range []string{"*.yaml", "*.yml"} {
		matches, err := afero.Glob(fs, filepath.Join(dir, pattern))
		if err != nil {
			return nil, fmt.Errorf("read directory %q: %w", dir, err)
		}
		files = append(files, matches...)
	}

	slices.Sort(files)

	return files, nil
}
