package web

import (
	"strconv"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

func queryInt(value, field string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, &archive.FieldError{Field: field, Message: field + " 必须为非负整数"}
	}
	return parsed, nil
}

func queryInt64(value, field string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, &archive.FieldError{Field: field, Message: field + " 必须为非负整数"}
	}
	return parsed, nil
}
