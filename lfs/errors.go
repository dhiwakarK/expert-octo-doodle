package lfs

import (
	"errors"
	"fmt"
	"runtime"
)

type WrappedError struct {
	Err     error
	Message string
	Panic   bool
	stack   []byte
	context map[string]string
}

func Error(err error) *WrappedError {
	return Errorf(err, "")
}

func Errorf(err error, format string, args ...interface{}) *WrappedError {
	if err == nil {
		return nil
	}

	e := &WrappedError{
		Err:     err,
		Message: err.Error(),
		Panic:   true,
		stack:   Stack(),
	}

	if len(format) > 0 {
		e.Errorf(format, args...)
	}

	return e
}

func (e *WrappedError) Set(key, value string) {
	if e.context == nil {
		e.context = make(map[string]string)
	}
	e.context[key] = value
}

func (e *WrappedError) Get(key string) string {
	if e.context == nil {
		return ""
	}
	return e.context[key]
}

func (e *WrappedError) Del(key string) {
	if e.context == nil {
		return
	}
	delete(e.context, key)
}

func (e *WrappedError) Error() string {
	return e.Message
}

func (e *WrappedError) Errorf(format string, args ...interface{}) {
	e.Message = fmt.Sprintf(format, args...)
}

func (e *WrappedError) InnerError() string {
	return e.Err.Error()
}

func (e *WrappedError) Stack() []byte {
	return e.stack
}

func (e *WrappedError) Context() map[string]string {
	if e.context == nil {
		e.context = make(map[string]string)
	}
	return e.context
}

func Stack() []byte {
	stackBuf := make([]byte, 1024*1024)
	written := runtime.Stack(stackBuf, false)
	return stackBuf[:written]
}

type notImplError struct {
	error
}

func (e notImplError) NotImplemented() bool {
	return true
}

func newNotImplError() error {
	return notImplError{errors.New("Not Implemented")}
}

func isNotImplError(err *WrappedError) bool {
	type notimplerror interface {
		NotImplemented() bool
	}
	if e, ok := err.Err.(notimplerror); ok {
		return e.NotImplemented()
	}
	return false
}

type retriableError struct {
	error
}

func (e retriableError) Retriable() bool {
	return true
}

func newRetriableError(err *WrappedError) *WrappedError {
	e := Error(retriableError{err})
	e.Panic = false
	return e
}

func isRetriableError(err *WrappedError) bool {
	type retriableerror interface {
		Retriable() bool
	}
	if e, ok := err.Err.(retriableerror); ok {
		return e.Retriable()
	}
	return false
}
