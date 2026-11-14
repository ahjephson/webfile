// Package clienterror translates diagnostics into messages safe for API clients.
package clienterror

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"syscall"
)

type Message string

const (
	Unknown      Message = "The operation could not be completed. Please try again or contact your administrator."
	NotFound     Message = "The file or folder no longer exists."
	Permission   Message = "You do not have permission to access this file or folder."
	Exists       Message = "The destination already exists or has changed. Refresh the folder and try again."
	NoSpace      Message = "There is not enough free disk space to complete the operation."
	Quota        Message = "Your storage quota has been reached."
	ReadOnly     Message = "The destination is read-only."
	InvalidPath  Message = "The file or folder path is not valid."
	NotDirectory Message = "The selected destination is not a folder."
	TooLarge     Message = "The file is too large to open in the editor."
	Cancelled    Message = "The operation was cancelled."
	Timeout      Message = "The operation timed out. Please try again."
	Incomplete   Message = "The transfer was interrupted. Please try again to resume it."
	Recovery     Message = "Some files have been retained for recovery. Contact your administrator before retrying."
	Cleanup      Message = "Cleanup could not be completed. Some files may remain; contact your administrator."
	Unavailable  Message = "Tasks are currently unavailable. Please try again."
	UndoChanged  Message = "Undo stopped because files or folders changed. Review the remaining items."
)

type diagnostic struct {
	text    string
	message Message
}

func (e *diagnostic) Error() string          { return e.text }
func (e *diagnostic) ClientMessage() Message { return e.message }

// New keeps an internal diagnostic and an explicitly selected public message.
func New(text string, message Message) error { return &diagnostic{text, message} }

func Text(err error) string {
	if err == nil {
		return ""
	}
	var friendly interface{ ClientMessage() Message }
	if errors.As(err, &friendly) {
		return Stored(string(friendly.ClientMessage()))
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return string(NotFound)
	case errors.Is(err, fs.ErrPermission):
		return string(Permission)
	case errors.Is(err, fs.ErrExist):
		return string(Exists)
	case errors.Is(err, syscall.ENOSPC):
		return string(NoSpace)
	case errors.Is(err, syscall.EDQUOT):
		return string(Quota)
	case errors.Is(err, syscall.EROFS):
		return string(ReadOnly)
	case errors.Is(err, syscall.ENOTDIR):
		return string(NotDirectory)
	case errors.Is(err, context.Canceled):
		return string(Cancelled)
	case errors.Is(err, context.DeadlineExceeded):
		return string(Timeout)
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return string(Incomplete)
	default:
		return string(Unknown)
	}
}

// Stored only accepts our fixed messages. Legacy persisted errors have lost
// their type information and must never be treated as safe diagnostic text.
func Stored(text string) string {
	switch Message(text) {
	case "", Unknown, NotFound, Permission, Exists, NoSpace, Quota, ReadOnly,
		InvalidPath, NotDirectory, TooLarge, Cancelled, Timeout, Incomplete,
		Recovery, Cleanup, Unavailable, UndoChanged:
		return text
	default:
		return string(Unknown)
	}
}
