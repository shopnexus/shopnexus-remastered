package domain

import (
	"regexp"
	"time"

	"shopnexus/internal/shared/errx"
)

var providerRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// OAuthIdentity links a provider's account to a local one. It deliberately holds no
// email: an address the provider asserts is matched against account.email and login
// merges into that account, so the address lives there and nowhere else.
type OAuthIdentity struct {
	ID        int64
	AccountID int64
	Provider  string
	// ProviderUID is the provider's stable subject id, never the email.
	ProviderUID string
	CreatedAt   time.Time
}

func NewOAuthIdentity(accountID int64, provider, uid string) (*OAuthIdentity, error) {
	if err := ValidateProvider(provider); err != nil {
		return nil, err
	}
	if uid == "" {
		return nil, errx.NewValidationError("invalid field: credential", errx.Field{
			Field: "credential", Rule: "required", Message: "the provider returned no subject id",
		})
	}
	return &OAuthIdentity{AccountID: accountID, Provider: provider, ProviderUID: uid}, nil
}

// ValidateProvider guards the kebab-case shape the column CHECKs, and is also the
// path parameter's validation on unlink.
func ValidateProvider(provider string) error {
	if providerRe.MatchString(provider) {
		return nil
	}
	return errx.NewValidationError("invalid field: provider", errx.Field{
		Field: "provider", Rule: "pattern", Message: "must be lowercase kebab-case, e.g. google",
	})
}
