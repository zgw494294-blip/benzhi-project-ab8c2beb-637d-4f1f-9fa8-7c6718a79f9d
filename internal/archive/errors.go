package archive

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound        = errors.New("档案不存在")
	ErrVersionConflict = errors.New("档案版本冲突")
	ErrInvalidState    = errors.New("当前状态不允许此操作")
)

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *FieldError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

type StateError struct {
	Status Status
	Action string
}

func (e *StateError) Error() string {
	return fmt.Sprintf("档案处于%s，不能执行%s", e.Status.Label(), e.Action)
}

func (e *StateError) Unwrap() error { return ErrInvalidState }

type ConsentConflictError struct {
	CurrentRevision int      `json:"currentRevision"`
	ChangedFields   []string `json:"changedFields"`
}

func (e *ConsentConflictError) Error() string {
	return fmt.Sprintf("授权修订号已变化，当前为 %d", e.CurrentRevision)
}

func (e *ConsentConflictError) Unwrap() error { return ErrVersionConflict }

type BatchValidationError struct {
	Kind  string       `json:"kind"`
	Items []BatchError `json:"items"`
}

func (e *BatchValidationError) Error() string {
	if len(e.Items) == 0 {
		return "批量请求校验失败"
	}
	return e.Items[0].Message
}
