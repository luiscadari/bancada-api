package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON stores a Go value in a jsonb column.
type JSON[T any] struct {
	Val T
}

// NewJSON wraps v for a jsonb column.
func NewJSON[T any](v T) JSON[T] { return JSON[T]{Val: v} }

// Data returns the wrapped value.
func (j JSON[T]) Data() T { return j.Val }

func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.Val)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (j *JSON[T]) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		var zero T
		j.Val = zero
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("database.JSON: cannot scan %T", src)
	}
	return json.Unmarshal(b, &j.Val)
}

// GormDataType makes GORM treat the field as jsonb.
func (JSON[T]) GormDataType() string { return "jsonb" }

func (j JSON[T]) MarshalJSON() ([]byte, error) { return json.Marshal(j.Val) }

func (j *JSON[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &j.Val) }
