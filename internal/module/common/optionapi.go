package common

import (
	"context"
	"fmt"

	"shopnexus/internal/shared/id"
)

// The published shape of the option registry, and the body behind it. Here rather than in each
// module's `api` package because the rows are shared DDL and the projection is the same wherever
// they live: two modules had started keeping identical copies of both.

// OptionDTO is one selectable entry. The staff-only fields are omitted for anyone else — which
// implementation serves a rail and whether a row is switched off is a map of who this platform pays,
// and a buyer needs the name and the id.
type OptionDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Provider    string `json:"provider,omitempty"`
	IsEnabled   *bool  `json:"is_enabled,omitempty"`
	Priority    *int   `json:"priority,omitempty"`
}

// OptionList is a whole category — one resource rather than a collection, which is why it is not
// paginated: a deployment offers a handful per category, and an operator surface that hides half of
// itself behind a cursor is one nobody trusts.
type OptionList struct {
	Options []OptionDTO `json:"options"`
	// Providers is what a row's `provider` may be set to here — the implementations this binary
	// has. Staff only, and the reason an admin can move a service from one courier to another
	// without guessing at a name.
	Providers []string `json:"providers,omitempty"`
}

// ListOptionsRequest reads one category. Admin asks for the staff view: every row including the
// disabled ones, with the provider on each.
type ListOptionsRequest struct {
	ActorID  id.ID[id.Account] `json:"-" validate:"required"`
	Category string            `json:"-" validate:"required"`
	Admin    bool              `json:"-"`
}

// SaveOptionRequest is the operator's edit. Absent leaves a field alone — the tri-state PATCH shape
// without the clearing half, since none of these is nullable.
//
// No id, category or slug change: the slug is permanent because a settled payment and a shipped
// parcel hold it as plain text, and moving a row between categories would change what those mean.
type SaveOptionRequest struct {
	ActorID     id.ID[id.Account] `json:"-" validate:"required"`
	ID          string            `json:"-" validate:"required"`
	Provider    *string           `json:"provider,omitempty" validate:"omitempty,max=100"`
	IsEnabled   *bool             `json:"is_enabled,omitempty"`
	Name        *string           `json:"name,omitempty" validate:"omitempty,max=200"`
	Description *string           `json:"description,omitempty" validate:"omitempty,max=1000"`
	Priority    *int              `json:"priority,omitempty" validate:"omitempty,gte=0,lte=1000"`
}

// ListOptions is the shared body: read the category, project it for whoever is asking. The caller
// has already decided whether this is the staff view — the role is a row in the account module's
// table, so only a module's service can learn it.
func ListOptions(ctx context.Context, store OptionStore, providers []string,
	req ListOptionsRequest) (OptionList, error) {
	if !CategoryVisibleTo(req.Category, req.Admin) {
		return OptionList{}, ErrOptionCategoryUnknown
	}
	list := store.ListEnabled
	if req.Admin {
		list = store.ListAll
	}
	rows, err := list(ctx, req.Category)
	if err != nil {
		return OptionList{}, fmt.Errorf("list %s options: %w", req.Category, err)
	}
	out := OptionList{Options: make([]OptionDTO, 0, len(rows))}
	for _, o := range rows {
		out.Options = append(out.Options, optionDTO(o, req.Admin))
	}
	if req.Admin {
		out.Providers = providers
	}
	return out, nil
}

// SaveOption applies an operator's edit. The provider is checked against what this binary actually
// has, because a row naming one nobody registered is a rail that cannot be charged and a parcel
// that cannot be booked — and the first person to find out would be a buyer at a checkout.
func SaveOption(ctx context.Context, store OptionStore, providers []string,
	req SaveOptionRequest) (OptionDTO, error) {
	row, err := store.Find(ctx, req.ID)
	if err != nil {
		return OptionDTO{}, fmt.Errorf("find option: %w", err)
	}
	if req.Provider != nil {
		if !contains(providers, *req.Provider) {
			return OptionDTO{}, ErrOptionProviderUnknown
		}
		row.Provider = *req.Provider
	}
	if req.IsEnabled != nil {
		row.IsEnabled = *req.IsEnabled
	}
	if req.Name != nil {
		row.Name = *req.Name
	}
	if req.Description != nil {
		row.Description = *req.Description
	}
	if req.Priority != nil {
		row.Priority = *req.Priority
	}
	if err := store.Save(ctx, row); err != nil {
		return OptionDTO{}, fmt.Errorf("save option: %w", err)
	}
	return optionDTO(row, true), nil
}

func optionDTO(o Option, staff bool) OptionDTO {
	out := OptionDTO{ID: o.ID, Name: o.Name, Description: o.Description}
	if staff {
		out.Provider = o.Provider
		out.IsEnabled = &o.IsEnabled
		out.Priority = &o.Priority
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
