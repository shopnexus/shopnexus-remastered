package response

import sharedmodel "shopnexus-remastered/internal/module/shared/model"

type CommonResponse struct {
	Data  any                `json:"data,omitempty"`
	Error *sharedmodel.Error `json:"error"`
}
