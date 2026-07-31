package domain

import (
	"strings"

	"shopnexus/internal/shared/validation"
)

// Category is one node of the browse tree. A nil ParentID is a root; deleting a parent
// promotes its children rather than removing them, which the FK does with SET NULL.
type Category struct {
	ID          int64
	ParentID    *int64
	Name        string `validate:"required,min=1,max=100"`
	Description string `validate:"max=2000"`
}

func NewCategory(name, description string, parentID *int64) (*Category, error) {
	c := &Category{
		ParentID:    parentID,
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate covers what one row can see. "No cycle anywhere up the chain" needs the whole
// ancestor path, so it is a guarded write in the adapter — only the database has the path.
func (c *Category) Validate() error {
	if err := validation.Default().Struct(c); err != nil {
		return validation.AsError(err)
	}
	if c.ParentID != nil && *c.ParentID == c.ID && c.ID != 0 {
		return ErrCategoryCycle
	}
	return nil
}
