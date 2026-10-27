package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// ManagedAppMessage is the error text of a change to an app a template
// instance manages.
const ManagedAppMessage = "this app is managed by a template instance; change it in the Winterflow UI"

// internalMessage is the text of an error that is not the caller's to fix;
// the details stay in the log.
const internalMessage = "internal error"

// UserID is the authenticated caller ("" when none: /mcp sits behind the
// auth middleware, so a tool treats that as an internal error).
func UserID(ctx context.Context) string {
	p, ok := webutil.PrincipalFrom(ctx)
	if !ok {
		return ""
	}
	return p.UserID
}

// ErrorResult is a tool error (isError) for err. A classified domain error
// (rejected, not found, conflict, forbidden) keeps its message, which says
// what to fix; a change to an app a template instance manages says to use
// the UI; anything else reads "internal error" (log it before).
func ErrorResult(err error) *sdk.CallToolResult {
	switch {
	case errors.Is(err, model.ErrManagedByTemplate):
		return ErrorText(ManagedAppMessage)
	case errors.Is(err, model.ErrRejected), errors.Is(err, model.ErrNotFound),
		errors.Is(err, model.ErrConflict), errors.Is(err, model.ErrForbidden):
		return ErrorText(err.Error())
	}
	return ErrorText(internalMessage)
}

// ErrorText is a tool error (isError) with msg as its text.
func ErrorText(msg string) *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: msg}}}
}

// JSONResult is a tool result whose text is v as compact JSON (HTML
// characters unescaped: log lines and compose files are full of them).
func JSONResult(v any) *sdk.CallToolResult {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ErrorText(internalMessage)
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))}}}
}
