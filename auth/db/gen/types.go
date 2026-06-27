package authdb

import (
	"database/sql/driver"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type UUID struct {
	Bytes [16]byte
	Valid bool
}

func (u *UUID) Scan(value any) error {
	if value == nil {
		*u = UUID{}
		return nil
	}
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return fmt.Errorf("scan uuid: unsupported %T", value)
	}
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	u.Bytes = [16]byte(id)
	u.Valid = true
	return nil
}

func (u UUID) Value() (driver.Value, error) {
	if !u.Valid {
		return nil, nil
	}
	return uuid.UUID(u.Bytes).String(), nil
}
