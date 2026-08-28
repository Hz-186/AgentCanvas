package errors

import "errors"

const (
	CodeBadRequest = 44000 + iota
	CodeUnauthorized
	CodeForbidden
	CodeNotFound
	_ // reserved: former CodeConflict slot, kept so later codes stay stable
	CodeInternal
	CodeDependencyUnavailable
)

var (
	ErrInvalidInput = errors.New("invalid input")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
)
