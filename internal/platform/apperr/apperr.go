// Package apperr defines the application error type rendered as problem+json.
//
// Codes are stable English identifiers for clients; Detail is a pt-BR sentence that tells
// the user what to do, following the Bancada voice guidelines.
package apperr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type Error struct {
	Status int
	Code   string
	Detail string
	Fields []FieldError
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Detail, e.Err)
	}
	return e.Code + ": " + e.Detail
}

func (e *Error) Unwrap() error { return e.Err }

func New(status int, code, detail string) *Error {
	return &Error{Status: status, Code: code, Detail: detail}
}

func Validation(detail string, fields ...FieldError) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "validation_failed", Detail: detail, Fields: fields}
}

func Field(field, message string) FieldError { return FieldError{Field: field, Message: message} }

func NotFound(code, detail string) *Error {
	return New(http.StatusNotFound, code, detail)
}

func Conflict(code, detail string) *Error {
	return New(http.StatusConflict, code, detail)
}

// Rule is a business rule violation (422).
func Rule(code, detail string) *Error {
	return New(http.StatusUnprocessableEntity, code, detail)
}

func Forbidden(code, detail string) *Error {
	return New(http.StatusForbidden, code, detail)
}

func Unauthorized(code, detail string) *Error {
	return New(http.StatusUnauthorized, code, detail)
}

var (
	ErrUnauthenticated = Unauthorized("unauthenticated", "Entre novamente para continuar.")
	ErrForbidden       = Forbidden("forbidden", "Seu perfil não permite esta ação. Peça ao dono da conta.")
)

// From converts any error into an *Error, translating well-known database errors.
// Unknown errors become a 500 that hides internal details.
func From(err error) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &Error{Status: http.StatusNotFound, Code: "not_found", Detail: "Registro não encontrado.", Err: err}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return &Error{Status: http.StatusConflict, Code: "already_exists",
				Detail: "Já existe um registro com estes dados.", Err: err}
		case "23503": // foreign_key_violation
			return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_reference",
				Detail: "Um dos registros informados não existe.", Err: err}
		case "23514", "23502": // check_violation, not_null_violation
			return &Error{Status: http.StatusBadRequest, Code: "validation_failed",
				Detail: "Confira os dados informados.", Err: err}
		case "42501": // insufficient_privilege, including RLS WITH CHECK violations
			return &Error{Status: http.StatusForbidden, Code: "forbidden",
				Detail: "Seu perfil não permite esta ação.", Err: err}
		}
	}
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error",
		Detail: "Algo deu errado do nosso lado. Tente de novo em instantes.", Err: err}
}
