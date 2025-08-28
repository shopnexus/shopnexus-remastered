package validator

import (
	"encoding/json"
	"fmt"

	sharedmodel "shopnexus-remastered/internal/module/backup/shared/model"

	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	entranslations "github.com/go-playground/validator/v10/translations/en"
)

type CustomValidator struct {
	uni       *ut.UniversalTranslator
	validator *validator.Validate
}

func New() (*CustomValidator, error) {
	en := en.New()
	uni := ut.New(en, en)
	validate := validator.New()

	// Register default translations (en)
	trans, _ := uni.GetTranslator("en")
	if err := entranslations.RegisterDefaultTranslations(validate, trans); err != nil {
		return nil, fmt.Errorf("failed to register translations: %w", err)
	}

	return &CustomValidator{
		uni:       uni,
		validator: validate,
	}, nil
}

func (cv *CustomValidator) Validate(i interface{}) error {
	err := cv.validator.Struct(i)
	if valErr, ok := err.(validator.ValidationErrors); ok {
		trans, _ := cv.uni.GetTranslator("en")
		text, err := json.Marshal(valErr.Translate(trans))
		if err != nil {
			// Fallback to the original validation error if JSON marshaling fails
			return valErr
		}

		return sharedmodel.NewError("validation", string(text))
	}

	return err
}
