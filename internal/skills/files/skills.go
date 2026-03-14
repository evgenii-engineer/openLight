package files

import (
	"context"
	"fmt"
	"strings"

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
