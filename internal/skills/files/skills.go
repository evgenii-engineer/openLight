package files

import (
	"context"
	"fmt"
	"strings"
	"time"

	"openlight/internal/skills"
)

type listSkill struct {
	manager Manager
}

func NewListSkill(manager Manager) skills.Skill {
	return &listSkill{manager: manager}
}

func (s *listSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "file_list",
		Group:       skills.GroupFiles,
		Description: "List files inside a whitelisted directory or show allowed roots.",
		Aliases:     []string{"files", "list files", "ls"},
		Usage:       "/files [path]",
		Examples: []string{
			"list /tmp/openlight",
			"file_list ./configs",
		},
	}
}

func (s *listSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	path := strings.TrimSpace(input.Args["path"])
	if path == "" {
		roots := s.manager.Roots()
		switch len(roots) {
		case 0:
			return skills.Result{Text: "No file roots are whitelisted."}, nil
		case 1:
			path = roots[0]
		default:
			lines := make([]string, 0, len(roots))
			for _, root := range roots {
				lines = append(lines, "- "+root)
			}
			return skills.Result{Text: "Allowed file roots:\n" + strings.Join(lines, "\n")}, nil
		}
	}

	result, err := s.manager.List(ctx, path)
	if err != nil {
		return skills.Result{}, err
	}
	if len(result.Entries) == 0 {
		return skills.Result{Text: "Directory is empty: " + result.Path}, nil
	}

	lines := make([]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		if entry.IsDir {
			lines = append(lines, "- "+entry.Name+"/")
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", entry.Name, formatSize(entry.Size)))
	}

	text := fmt.Sprintf("Files in %s:\n%s", result.Path, strings.Join(lines, "\n"))
	if result.Truncated {
		text += fmt.Sprintf("\n\nShowing first %d entries.", len(result.Entries))
	}
	return skills.Result{Text: text}, nil
}

type readSkill struct {
	manager Manager
}

func NewReadSkill(manager Manager) skills.Skill {
	return &readSkill{manager: manager}
}

func (s *readSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "file_read",
		Group:       skills.GroupFiles,
		Description: "Read a text file from a whitelisted path.",
		Aliases:     []string{"read file", "show file", "cat"},
		Usage:       "/read <path>",
		Examples: []string{
			"read /etc/hostname",
			"file_read ./README.md",
		},
	}
}

func (s *readSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "path", Prompt: "Which file to read?", Placeholder: "./README.md"},
		},
	}
}

func (s *readSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	result, err := s.manager.Read(ctx, input.Args["path"])
	if err != nil {
		return skills.Result{}, err
	}

	content := result.Content
	if content == "" {
		content = "(empty file)"
	}

	text := fmt.Sprintf("Contents of %s:\n%s", result.Path, content)
	if result.Truncated {
		text += "\n\n(truncated)"
	}
	return skills.Result{Text: text}, nil
}

type searchSkill struct {
	manager Manager
}

func NewSearchSkill(manager Manager) skills.Skill {
	return &searchSkill{manager: manager}
}

func (s *searchSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "file_search",
		Group:       skills.GroupFiles,
		Description: "Search text across whitelisted files.",
		Aliases:     []string{"search files", "find in files", "grep"},
		Usage:       "/file_search <pattern> [in <path>]",
		Examples: []string{
			"file_search OPENAI_API_KEY",
			"search files tailscale in ./logs",
		},
	}
}

func (s *searchSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "pattern", Prompt: "What text to search for?", Placeholder: "TODO"},
		},
	}
}

func (s *searchSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	result, err := s.manager.Search(ctx, input.Args["pattern"], input.Args["path"])
	if err != nil {
		return skills.Result{}, err
	}
	if len(result.Matches) == 0 {
		return skills.Result{Text: "No matches found."}, nil
	}

	lines := make([]string, 0, len(result.Matches))
	for _, match := range result.Matches {
		lines = append(lines, fmt.Sprintf("- %s:%d %s", match.Path, match.Line, match.Preview))
	}

	text := "Matches:\n" + strings.Join(lines, "\n")
	if result.Truncated {
		text += fmt.Sprintf("\n\nShowing first %d matches.", len(result.Matches))
	}
	return skills.Result{Text: text}, nil
}

type statSkill struct {
	manager Manager
}

func NewStatSkill(manager Manager) skills.Skill {
	return &statSkill{manager: manager}
}

func (s *statSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "file_stat",
		Group:       skills.GroupFiles,
		Description: "Show metadata for a whitelisted file or directory.",
		Aliases:     []string{"file info", "file stat", "metadata"},
		Usage:       "/file_stat <path>",
	}
}

func (s *statSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "path", Prompt: "Which file or directory to stat?", Placeholder: "./README.md"},
		},
	}
}

func (s *statSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	result, err := s.manager.Stat(ctx, input.Args["path"])
	if err != nil {
		return skills.Result{}, err
	}

	kindLabel := "File"
	if result.IsDir {
		kindLabel = "Directory"
	}
	return skills.Result{
		Text: fmt.Sprintf(
			"%s: %s\nSize: %s\nMode: %s\nModified: %s",
			kindLabel,
			result.Path,
			formatSize(result.Size),
			result.Mode,
			result.ModifiedAt.Format(time.RFC3339),
		),
	}, nil
}

type writeSkill struct {
	manager Manager
}

func NewWriteSkill(manager Manager) skills.Skill {
	return &writeSkill{manager: manager}
}

func (s *writeSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "file_write",
		Group:       skills.GroupFiles,
		Description: "Create or overwrite a text file inside a whitelisted path.",
		Aliases:     []string{"write file", "create file"},
		Usage:       "/write <path> :: <content>",
		Examples: []string{
			"write /tmp/openlight/test.txt :: hello",
			"file_write ./scratch.py :: print('hi')",
		},
		Mutating: true,
	}
}

func (s *writeSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "path", Prompt: "Path to write?", Placeholder: "/tmp/openlight/test.txt"},
			{Name: "content", Prompt: "What should the file contain?", Placeholder: "hello"},
		},
	}
}

func (s *writeSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	result, err := s.manager.Write(ctx, input.Args["path"], input.Args["content"])
	if err != nil {
		return skills.Result{}, err
	}

	verb := "Updated"
	if result.Created {
		verb = "Created"
	}
	return skills.Result{Text: fmt.Sprintf("%s file: %s (%d bytes)", verb, result.Path, result.Bytes)}, nil
}

type replaceSkill struct {
	manager Manager
}

func NewReplaceSkill(manager Manager) skills.Skill {
	return &replaceSkill{manager: manager}
}

func (s *replaceSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "file_replace",
		Group:       skills.GroupFiles,
		Description: "Replace text inside a whitelisted file.",
		Aliases:     []string{"replace in file", "file replace"},
		Usage:       "/replace <old> with <new> in <path>",
		Examples: []string{
			"replace 8080 with 8081 in ./config.yaml",
			"file_replace ./config.yaml :: 8080 => 8081",
		},
		Mutating: true,
	}
}

func (s *replaceSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "path", Prompt: "Which file to edit?", Placeholder: "./config.yaml"},
			{Name: "find", Prompt: "Text to find?", Placeholder: "8080"},
			{Name: "replace", Prompt: "Replace with?", Placeholder: "8081"},
		},
	}
}

func (s *replaceSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	result, err := s.manager.Replace(ctx, input.Args["path"], input.Args["find"], input.Args["replace"])
	if err != nil {
		return skills.Result{}, err
	}

	return skills.Result{
		Text: fmt.Sprintf("Replaced %d occurrence(s) in %s", result.Replacements, result.Path),
	}, nil
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	suffixes := []string{"KiB", "MiB", "GiB", "TiB"}
	if exp >= len(suffixes) {
		exp = len(suffixes) - 1
	}
	value := float64(size) / float64(div)
	return fmt.Sprintf("%.1f %s", value, suffixes[exp])
}
