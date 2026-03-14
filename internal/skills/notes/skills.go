package notes

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"openlight/internal/models"
	"openlight/internal/skills"
)

type Store interface {
	AddNote(ctx context.Context, text string) (models.Note, error)
	ListNotes(ctx context.Context, limit int) ([]models.Note, error)
	DeleteNote(ctx context.Context, id int64) error
}

type addSkill struct {
	store Store
}

func NewAddSkill(store Store) skills.Skill {
	return &addSkill{store: store}
}

func (s *addSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "note_add",
		Group:       skills.GroupNotes,
		Description: "Add a short note to SQLite storage.",
		Aliases:     []string{"add note", "remember"},
		Usage:       "/note <text>",
		Mutating:    true,
	}
}

func (s *addSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	text := strings.TrimSpace(input.Args["text"])
	if text == "" {
		return skills.Result{}, fmt.Errorf("%w: note text is required", skills.ErrInvalidArguments)
	}

	note, err := s.store.AddNote(ctx, text)
	if err != nil {
		return skills.Result{}, err
	}

	return skills.Result{Text: fmt.Sprintf("Saved note #%d", note.ID)}, nil
}

type listSkill struct {
	store Store
	limit int
}

func NewListSkill(store Store, limit int) skills.Skill {
	return &listSkill{
		store: store,
		limit: limit,
	}
}

func (s *listSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "note_list",
		Group:       skills.GroupNotes,
		Description: "List the latest saved notes.",
		Aliases:     []string{"notes", "list notes"},
		Usage:       "/notes",
	}
}

func (s *listSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	notes, err := s.store.ListNotes(ctx, s.limit)
	if err != nil {
		return skills.Result{}, err
	}
	if len(notes) == 0 {
		return skills.Result{Text: "No notes saved yet."}, nil
	}

	lines := make([]string, 0, len(notes))
	for _, note := range notes {
		lines = append(lines, fmt.Sprintf("- #%d %s", note.ID, note.Text))
	}

	return skills.Result{Text: "Notes:\n" + strings.Join(lines, "\n")}, nil
}

type deleteSkill struct {
	store Store
}

func NewDeleteSkill(store Store) skills.Skill {
	return &deleteSkill{store: store}
}

func (s *deleteSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "note_delete",
		Group:       skills.GroupNotes,
		Description: "Delete a saved note by id.",
		Aliases:     []string{"delete note", "remove note", "note delete"},
		Usage:       "/note_delete <id>",
		Mutating:    true,
	}
}

func (s *deleteSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	rawID := strings.TrimSpace(input.Args["id"])
	if rawID == "" {
		return skills.Result{}, fmt.Errorf("%w: note id is required", skills.ErrInvalidArguments)
	}

	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return skills.Result{}, fmt.Errorf("%w: note id must be a positive integer", skills.ErrInvalidArguments)
	}

	if err := s.store.DeleteNote(ctx, id); err != nil {
		return skills.Result{}, err
	}

	return skills.Result{Text: fmt.Sprintf("Deleted note #%d", id)}, nil
}
