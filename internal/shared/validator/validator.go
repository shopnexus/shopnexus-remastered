package validator

import (
	"database/sql/driver"
	stderrors "errors"
	"fmt"
	"reflect"
	"shopnexus-server/internal/shared/errors"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	entranslations "github.com/go-playground/validator/v10/translations/en"
	"github.com/google/uuid"
	"github.com/guregu/null/v6"
)

var (
	once     sync.Once
	validate *CustomValidator
)

type CustomValidator struct {
	uni       *ut.UniversalTranslator
	validator *validator.Validate
}

func New() (*CustomValidator, error) {
	en := en.New()
	uni := ut.New(en, en)
	validate := validator.New(
		validator.WithRequiredStructEnabled(),
	)

	// Register default translations (en)
	trans, _ := uni.GetTranslator("en")
	if err := entranslations.RegisterDefaultTranslations(validate, trans); err != nil {
		return nil, fmt.Errorf("failed to register translations: %w", err)
	}

	// Register custom validation tags
	if err := validate.RegisterValidation("iso4217", Iso4217); err != nil {
		return nil, fmt.Errorf("failed to register iso4217 validator: %w", err)
	}

	// Register all null.* types to use the ValidateNullable CustomTypeFunc
	validate.RegisterCustomTypeFunc(
		ParseNullable,
		null.Bool{},
		null.Byte{},
		null.Float{},
		null.Int16{},
		null.Int32{},
		null.Int64{},
		null.String{},
		null.Time{},
		// uuid.UUID{}, // uuid.UUID has TextUnmarshaler implemented, no need to register
		uuid.NullUUID{},
	)

	return &CustomValidator{
		uni:       uni,
		validator: validate,
	}, nil
}

func (cv *CustomValidator) Validate(i any) error {
	err := cv.validator.Struct(i)
	var valErr validator.ValidationErrors
	if stderrors.As(err, &valErr) {
		trans, _ := cv.uni.GetTranslator("en")
		text, err := sonic.Marshal(valErr.Translate(trans))
		if err != nil {
			// Fallback to the original validation error if JSON marshaling fails
			return valErr
		}

		return errors.ErrValidation.Fmt(string(text))
	}

	return err
}

type Nullable interface {
	driver.Valuer
}

// Workaround for omitnil not working with "untyped nil"
// https://github.com/go-playground/validator/issues/1209#issuecomment-1892359649
var nilValue *struct{}

// ParseNullable implements validator.CustomTypeFunc.
func ParseNullable(field reflect.Value) any {
	if nullValue, ok := field.Interface().(Nullable); ok {
		if val, err := nullValue.Value(); err == nil {
			if val == nil {
				return nilValue // Return typed nil to indicate "nil" value
			}
			return val
		}
	}

	return nil // Return untyped nil means we tell the validator to throw error (because we cannot parse the value)
}

// Export shortcut to get the singleton validator instance, support restate terminal error.
func Validate(i any) error {
	once.Do(func() {
		var err error
		validate, err = New()
		if err != nil {
			panic(fmt.Sprintf("failed to create validator: %v", err))
		}
	})

	return validate.Validate(i)
}

// Unmarshal unmarshal JSON data into a struct and validate the result.
func Unmarshal(data []byte, v any) error {
	err := sonic.Unmarshal(data, v)
	if err != nil {
		return err
	}
	return Validate(v)
}
