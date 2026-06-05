package inventorymodel

import (
	"net/http"
	"shopnexus-server/internal/shared/errors"
)

// Sentinel errors for the inventory module.
var (
	ErrSerialCountMismatch = errors.NewError(
		http.StatusBadRequest,
		"serial_count_mismatch",
		"The number of serial IDs must match the quantity",
	)
	ErrInsufficientReservedInventory = errors.NewError(
		http.StatusConflict,
		"insufficient_reserved_inventory",
		"insufficient reserved inventory to release",
	)
	ErrOutOfStock = errors.NewErrorf(
		http.StatusConflict,
		"out_of_stock",
		"Sorry, this %s is out of stock right now (requested %d, only %d available)",
	)
	ErrOutOfStockRace = errors.NewErrorf(
		http.StatusConflict,
		"out_of_stock_race",
		"This %s was just reserved by someone else. Please try again.",
	)
	ErrSerialShortage = errors.NewErrorf(
		http.StatusConflict,
		"serial_shortage",
		"Only %d unit(s) of this %s have a serial available (requested %d)",
	)
)
