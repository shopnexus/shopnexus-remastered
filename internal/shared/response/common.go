package response

import "shopnexus-server/internal/shared/errors"

type CommonResponse struct {
	Data  any            `json:"data,omitempty"`
	Error *errors.Errorf `json:"error,omitempty"`
}
