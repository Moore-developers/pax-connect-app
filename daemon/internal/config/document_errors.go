package config

import "errors"

var (
	ErrRouteDocument   = errors.New("route_invalid")
	ErrDocumentField   = errors.New("unsupported_field")
	ErrCatalogDocument = errors.New("catalog_invalid")
	ErrCatalogWrite    = errors.New("route_save_failed")
)
