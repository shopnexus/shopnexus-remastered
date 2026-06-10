package commonecho

import (
	"errors"
	"fmt"
	"net/http"

	commonbiz "shopnexus-server/internal/module/common/biz"
	authclaims "shopnexus-server/internal/shared/claims"
	"shopnexus-server/internal/shared/response"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// UploadFile handles simple multipart/form-data upload.
func (h *Handler) UploadFile(c echo.Context) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, fmt.Errorf("missing file: %w", err))
	}
	src, err := fileHeader.Open()
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusBadRequest, err)
	}
	defer src.Close()

	private := c.FormValue("private")

	claims, err := authclaims.GetClaims(c.Request())
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusUnauthorized, err)
	}

	result, err := h.handler.UploadFile(c.Request().Context(), commonbiz.UploadFileParams{
		Account:     claims.Account,
		File:        src,
		Filename:    fileHeader.Filename,
		ContentType: fileHeader.Header.Get("Content-Type"),
		Size:        fileHeader.Size,
		Private:     private == "true",
	})
	if err != nil {
		return response.FromError(c.Response().Writer, http.StatusInternalServerError, err)
	}

	// Get the full resource details
	resourceMap, _ := h.biz.GetResourcesByIDs(c.Request().Context(), []uuid.UUID{result.ResourceID})
	resource, ok := resourceMap[result.ResourceID]
	if !ok {
		return response.FromError(
			c.Response().Writer,
			http.StatusInternalServerError,
			errors.New("failed to retrieve uploaded resource"),
		)
	}

	return response.FromDTO(c.Response().Writer, http.StatusOK, resource)
}
