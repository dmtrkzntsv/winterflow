package tools

import (
	"context"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	appuc "winterflow/internal/domain/usecase/app"
)

type createDomainInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	Name     string `json:"name" jsonschema:"an exact hostname (app.example.com) or a wildcard of one label (*.example.com)"`
	TLS      string `json:"tls" jsonschema:"auto: a certificate per hostname from Let's Encrypt; off: plain HTTP behind a tunnel or proxy that ends TLS"`
}

type deleteDomainInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	Name     string `json:"name" jsonschema:"the registered domain, from list_domains"`
}

func (t *tools) domains(s *sdk.Server) {
	add(s, "list_domains", "The domains registered on a server, each with its TLS mode and the apps using it. "+
		"Every ingress hostname must be covered by one.", readOnly, t.listDomains)
	add(s, "create_domain", "Register a domain on a server (or change its TLS mode), so apps there can route "+
		"hostnames it covers. An uploaded certificate is set in the Winterflow UI.", nil, t.createDomain)
	add(s, "delete_domain", "Remove a domain registration; refused while an app uses it.",
		&sdk.ToolAnnotations{DestructiveHint: ptr(true)}, t.deleteDomain)
}

func (t *tools) listDomains(ctx context.Context, in serverInput) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeDomainList, "list domains",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.ListServerDomainsObserved(ctx, userID, in.ServerID, o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}

func (t *tools) createDomain(ctx context.Context, in createDomainInput) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	tls := model.DomainTLS(in.TLS)
	if tls != model.DomainTLSAuto && tls != model.DomainTLSOff {
		return webmcp.ErrorText("tls must be auto or off (a certificate is uploaded in the Winterflow UI)")
	}
	req := command.CreateDomainRequest{Name: strings.TrimSpace(in.Name), TLS: tls}
	if err := req.Validate(); err != nil {
		return webmcp.ErrorText(err.Error())
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeDomainCreate, "add domain",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.CreateDomainObserved(ctx, userID, in.ServerID, req, o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}

func (t *tools) deleteDomain(ctx context.Context, in deleteDomainInput) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	req := command.DeleteDomainRequest{Name: strings.TrimSpace(in.Name)}
	if err := req.Validate(); err != nil {
		return webmcp.ErrorText(err.Error())
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeDomainDelete, "remove domain",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.DeleteDomainObserved(ctx, userID, in.ServerID, req.Name, o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}
