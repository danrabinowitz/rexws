package config

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Status is the stable identifier of a workflow stage. It is what state.json
// stores and what scripts pass to --status; labels are for display.
type Status string

const (
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusInReview   Status = "in_review"
	StatusReady      Status = "ready"
	StatusDone       Status = "done"
)

// StatusDef is one configured workflow stage.
type StatusDef struct {
	ID    Status `json:"id"`
	Label string `json:"label"`
}

// DefaultStatuses returns the built-in stages and their display labels.
func DefaultStatuses() []StatusDef {
	return []StatusDef{
		{ID: StatusInProgress, Label: "In Progress"},
		{ID: StatusBlocked, Label: "Blocked"},
		{ID: StatusInReview, Label: "In Review"},
		{ID: StatusReady, Label: "Ready"},
		{ID: StatusDone, Label: "Done"},
	}
}

var statusIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// StatusIDs returns the configured stage IDs in dashboard order.
func (c Config) StatusIDs() []Status {
	ids := make([]Status, len(c.Statuses))
	for i, def := range c.Statuses {
		ids[i] = def.ID
	}
	return ids
}

// StatusLabel returns the display label for a stage, falling back to the ID
// when the stage is not configured.
func (c Config) StatusLabel(status Status) string {
	for _, def := range c.Statuses {
		if def.ID == status {
			return def.Label
		}
	}
	return string(status)
}

// DefaultStatusValue returns the stage used for worktrees with no stored
// status.
func (c Config) DefaultStatusValue() Status {
	if c.DefaultStatus != "" {
		return c.DefaultStatus
	}
	for _, def := range c.Statuses {
		if def.ID == StatusReady {
			return StatusReady
		}
	}
	if len(c.Statuses) > 0 {
		return c.Statuses[0].ID
	}
	return StatusReady
}

// ParseStatus resolves a stage by ID or display label, ignoring case. IDs win
// when a value matches both.
func (c Config) ParseStatus(value string) (Status, error) {
	value = strings.TrimSpace(value)
	for _, def := range c.Statuses {
		if string(def.ID) == value {
			return def.ID, nil
		}
	}
	lower := strings.ToLower(value)
	for _, def := range c.Statuses {
		if strings.ToLower(string(def.ID)) == lower {
			return def.ID, nil
		}
	}
	for _, def := range c.Statuses {
		if strings.ToLower(def.Label) == lower {
			return def.ID, nil
		}
	}
	return "", fmt.Errorf("unknown status %q (want one of %s)", value, c.statusChoices())
}

// applyStatusDefaults fills stages, labels, and the default status for a
// config that omits them.
func (c *Config) applyStatusDefaults() {
	if len(c.Statuses) == 0 {
		c.Statuses = DefaultStatuses()
	}
	for i := range c.Statuses {
		if strings.TrimSpace(c.Statuses[i].Label) == "" {
			c.Statuses[i].Label = humanizeStatus(c.Statuses[i].ID)
		}
	}
	if c.DefaultStatus == "" {
		c.DefaultStatus = c.Statuses[0].ID
		for _, def := range c.Statuses {
			if def.ID == StatusReady {
				c.DefaultStatus = StatusReady
				break
			}
		}
	}
}

func (c Config) validateStatuses() error {
	if len(c.Statuses) == 0 {
		return fmt.Errorf("at least one workflow status is required")
	}
	ids := make(map[Status]bool, len(c.Statuses))
	labels := make(map[string]Status, len(c.Statuses))
	for i, def := range c.Statuses {
		if def.ID == "" {
			return fmt.Errorf("status %d has no id", i)
		}
		if !statusIDPattern.MatchString(string(def.ID)) {
			return fmt.Errorf("status id %q must start with a letter or digit and contain only lowercase letters, digits, '-' or '_'", def.ID)
		}
		if ids[def.ID] {
			return fmt.Errorf("status id %q appears twice", def.ID)
		}
		ids[def.ID] = true
		if strings.TrimSpace(def.Label) == "" {
			return fmt.Errorf("status %q has no label", def.ID)
		}
		key := strings.ToLower(def.Label)
		if other, ok := labels[key]; ok {
			return fmt.Errorf("status labels %q and %q are the same ignoring case", other, def.Label)
		}
		labels[key] = def.ID
	}
	if c.DefaultStatus == "" {
		return fmt.Errorf("default_status is required")
	}
	if !ids[c.DefaultStatus] {
		return fmt.Errorf("default_status %q is not one of the configured statuses", c.DefaultStatus)
	}
	return nil
}

func (c Config) statusChoices() string {
	choices := make([]string, 0, len(c.Statuses))
	for _, def := range c.Statuses {
		choices = append(choices, fmt.Sprintf("%s (%s)", def.ID, def.Label))
	}
	return strings.Join(choices, ", ")
}

// humanizeStatus turns "in_progress" into "In Progress".
func humanizeStatus(id Status) string {
	words := strings.FieldsFunc(string(id), func(r rune) bool { return r == '_' || r == '-' })
	for i, word := range words {
		runes := []rune(word)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}
