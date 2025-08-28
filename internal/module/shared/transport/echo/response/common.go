package response

import sharedmodel "shopnexus-remastered/internal/module/backup/shared/model"

type CommonResponse struct {
	Data  any               `json:"data,omitempty"`
	Error sharedmodel.Error `json:"error,omitempty"`
}
