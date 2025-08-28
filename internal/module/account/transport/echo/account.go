package accountecho

import (
	accountbiz "shopnexus-remastered/internal/module/account/biz"
)

type Handler struct {
	biz *accountbiz.AccountBiz
}

func NewHandler(biz *accountbiz.AccountBiz) *Handler {
	return &Handler{
		biz: biz,
	}
}