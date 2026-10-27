package tools

import (
	"context"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/cmdresult"
	"winterflow/internal/domain/usecase/secretreq"
)

// Messages the save checks answer with.
const (
	plainSecretMessage  = "secret values cannot be sent over MCP; use request_secrets"
	requestSecretsHint  = " Use request_secrets to ask the user for it."
	invalidAppIDMessage = "invalid app_id (take app ids from list_apps)"
)

// Log tails: the default and the most a call may ask for.
const (
	defaultLogTail = 200
	maxLogTail     = 5000
)

type serverInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
}

type appInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID    string `json:"app_id" jsonschema:"the app, from list_apps"`
}

type fileInput struct {
	Name      string `json:"name" jsonschema:"the file's path in the app, e.g. compose.yml or config/nginx.conf"`
	Content   string `json:"content" jsonschema:"the file's text; for a secret file only <encrypted>, which keeps the stored file"`
	Encrypted bool   `json:"encrypted,omitempty" jsonschema:"a secret file (its content can only be <encrypted>)"`
}

type variableInput struct {
	Name      string `json:"name" jsonschema:"the variable's name, as compose interpolates it: ${NAME}"`
	Value     string `json:"value" jsonschema:"the plain value; for a secret only <encrypted>, which keeps the stored value"`
	Encrypted bool   `json:"encrypted,omitempty" jsonschema:"a secret variable (its value can only be <encrypted>; new values come from request_secrets)"`
}

type sourceInput struct {
	RepoURL     string `json:"repo_url" jsonschema:"the git repository to deploy from (a stored access token is kept)"`
	Branch      string `json:"branch" jsonschema:"the branch to follow"`
	ComposePath string `json:"compose_path,omitempty" jsonschema:"the compose file inside the repository (default: compose's own lookup)"`
	AutoUpdate  bool   `json:"auto_update,omitempty" jsonschema:"redeploy when the branch moves"`
	PollSeconds int    `json:"poll_seconds,omitempty" jsonschema:"how often to look for new commits (default 120)"`
}

type ingressDomainInput struct {
	Domain       string `json:"domain" jsonschema:"the hostname; a domain registered on the server (list_domains) must cover it"`
	Expose       string `json:"expose,omitempty" jsonschema:"the http expose entry it routes to, <service>:<port> from x-winterflow.expose"`
	UpstreamPort int    `json:"upstream_port,omitempty" jsonschema:"or: a host port the compose file publishes itself (exactly one of expose and upstream_port)"`
}

type ingressRedirectInput struct {
	Domain string `json:"domain" jsonschema:"the hostname to redirect from"`
	Path   string `json:"path,omitempty" jsonschema:"only this path (default: the whole hostname)"`
	To     string `json:"to" jsonschema:"the target URL"`
	Code   int    `json:"code" jsonschema:"301, 302, 307 or 308"`
}

type ingressInput struct {
	Domains   []ingressDomainInput   `json:"domains" jsonschema:"hostnames routed to the app"`
	Redirects []ingressRedirectInput `json:"redirects,omitempty" jsonschema:"hostnames or paths redirected elsewhere"`
}

type saveAppInput struct {
	ServerID    string          `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID       string          `json:"app_id,omitempty" jsonschema:"the app to change, from list_apps; omit to create an app"`
	Name        string          `json:"name" jsonschema:"the app's display name"`
	Description string          `json:"description,omitempty" jsonschema:"a short description"`
	Icon        string          `json:"icon,omitempty" jsonschema:"an icon name shown in the UI"`
	Color       string          `json:"color,omitempty" jsonschema:"a color such as #3b82f6"`
	Files       []fileInput     `json:"files,omitempty" jsonschema:"every file of the app, compose.yml included (none for a git-sourced app); files left out are removed"`
	Variables   []variableInput `json:"variables,omitempty" jsonschema:"the app's variables; variables left out are removed"`
	Source      *sourceInput    `json:"source,omitempty" jsonschema:"deploy from a git repository instead of files"`
	Ingress     *ingressInput   `json:"ingress,omitempty" jsonschema:"hostnames routed to the app; omit for none (on an edit, omitting it removes the app's hostnames)"`
	Draft       bool            `json:"draft,omitempty" jsonschema:"save the change without deploying it"`
	SecretsFrom string          `json:"secrets_from,omitempty" jsonschema:"a submitted secret request (request_secrets, get_secret_request) whose values become these secret variables; each must be a secret the save declares (x-winterflow variables with encrypted: true, or a variable sent with encrypted: true); a variable it fills may be left out"`
}

type controlAppInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID    string `json:"app_id" jsonschema:"the app, from list_apps"`
	Action   string `json:"action" jsonschema:"start, stop, restart, or update (pull newer images, or the git source, and redeploy)"`
}

type renameAppInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID    string `json:"app_id" jsonschema:"the app, from list_apps"`
	Name     string `json:"name" jsonschema:"the new display name"`
}

type deleteAppInput struct {
	ServerID      string `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID         string `json:"app_id" jsonschema:"the app, from list_apps"`
	DeleteVolumes bool   `json:"delete_volumes,omitempty" jsonschema:"also remove the app's named Docker volumes: its data is gone"`
	DeleteBackups bool   `json:"delete_backups,omitempty" jsonschema:"also delete the app's backups"`
}

type rollbackAppInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID    string `json:"app_id" jsonschema:"the app, from list_apps"`
	Revision string `json:"revision" jsonschema:"the revision hash to restore, from get_app_revisions"`
}

type appStatusInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID    string `json:"app_id,omitempty" jsonschema:"one app, from list_apps (default: every app on the server)"`
}

type logsInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	AppID    string `json:"app_id" jsonschema:"the app, from list_apps"`
	Service  string `json:"service,omitempty" jsonschema:"only this compose service's lines"`
	Tail     int    `json:"tail,omitempty" jsonschema:"how many of the newest lines per container (default 200, at most 5000)"`
}

type imageInput struct {
	ServerID string `json:"server_id" jsonschema:"the server, from list_servers"`
	Image    string `json:"image" jsonschema:"an image reference without a tag, e.g. nginx or ghcr.io/owner/app"`
}

func (t *tools) apps(s *sdk.Server) {
	add(s, "list_apps", "The apps on a server, as its agent reports them, with each one's container status.", readOnly, t.listApps)
	add(s, "get_app", "An app's files, variables (secret values read <encrypted>), git source, ingress, the variables "+
		"and ports its compose files declare under x-winterflow, and its backups. Read it before editing with save_app.",
		readOnly, t.getApp)
	add(s, "save_app", "Create or change an app and deploy it (or only save it, with draft), waiting for the outcome. "+
		"Send the app's full state: files, variables and ingress left out are removed (call get_app first when editing). "+
		"A secret variable or file is sent only as <encrypted>, which keeps the value the app already stores under that "+
		"name (a new app or a renamed secret has none); new secret values come from the user through request_secrets, then "+
		"secrets_from: <request_id>. Answers the agent's result and the app's latest container status. Every answer "+
		"after the save was sent carries the app_id (also an error or a running one: a new app may already exist on the "+
		"server); pass app_id from the previous answer when retrying, or a second app is created.",
		nil, t.saveApp)
	add(s, "control_app", "Start, stop, restart or update (pull and redeploy) an app, waiting for the outcome.", nil, t.controlApp)
	add(s, "rename_app", "Change an app's display name.", nil, t.renameApp)
	add(s, "delete_app", "Delete an app: its containers and files. delete_volumes also removes its data volumes, "+
		"delete_backups its backups.", &sdk.ToolAnnotations{DestructiveHint: ptr(true)}, t.deleteApp)
	add(s, "rollback_app", "Restore an earlier revision of an app (get_app_revisions) as a new revision and redeploy it.", nil, t.rollbackApp)
	add(s, "get_app_status", "Container status from the server's latest report (no command is sent): one app, or "+
		"every app on the server. Poll it after a deploy.", readOnly, t.getAppStatus)
	add(s, "get_app_logs", "The newest log lines of an app's containers.", readOnly, t.getAppLogs)
	add(s, "get_app_revisions", "An app's revisions (every save is one), newest first, with the current and the deployed one.",
		readOnly, t.getAppRevisions)
	add(s, "get_image_tags", "The tags of a container image, read from its registry by the server's agent.", readOnly, t.getImageTags)
}

func ptr[T any](v T) *T { return &v }

// requireAppID refuses an app id that is not a single safe path element
// (the agent's own rule), before anything is dispatched.
func requireAppID(appID string) *sdk.CallToolResult {
	if appID == "" {
		return webmcp.ErrorText("app_id is required")
	}
	if !command.ValidAppID(appID) {
		return webmcp.ErrorText(invalidAppIDMessage)
	}
	return nil
}

// appTarget checks a server-and-app input: the ids and the ownership.
func (t *tools) appTarget(ctx context.Context, in appInput) (string, *sdk.CallToolResult) {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return "", fail
	}
	if fail := requireAppID(in.AppID); fail != nil {
		return "", fail
	}
	return userID, nil
}

// listedApp is one app in list_apps.
type listedApp struct {
	model.App
	Status *command.AppStatus `json:"status"`
}

func (t *tools) listApps(ctx context.Context, in serverInput) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppsList, "list apps",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.RefreshAppsObserved(ctx, userID, in.ServerID, o)
		})
	if fail != nil {
		return fail
	}
	var listed command.ListAppsResponse
	if err := json.Unmarshal(rec.Result, &listed); err != nil {
		return result(rec)
	}
	out := make([]listedApp, 0, len(listed.Apps))
	for _, a := range listed.Apps {
		out = append(out, listedApp{App: a, Status: t.appStatus(in.ServerID, a.ID)})
	}
	answer := map[string]any{"apps": out}
	if isTruncated(rec.Result) {
		answer["truncated"] = true
		answer["note"] = "the list was too large to keep whole: some apps are missing; see the Winterflow UI"
	}
	return webmcp.JSONResult(answer)
}

// item is a file or variable as get_app shows it.
type item struct {
	Name      string `json:"name"`
	Content   string `json:"content,omitempty"`
	Value     string `json:"value,omitempty"`
	Encrypted bool   `json:"encrypted"`
}

// appView is get_app's answer: the agent's app.get with its byte fields
// decoded and every secret masked.
type appView struct {
	AppID               string                         `json:"app_id"`
	Config              json.RawMessage                `json:"config"`
	Files               []item                         `json:"files"`
	Variables           []item                         `json:"variables"`
	Source              *sourceView                    `json:"source,omitempty"`
	DeclaredVariables   []xwinterflow.DeclaredVariable `json:"declared_variables,omitempty"`
	Expose              []command.ExposedPort          `json:"expose,omitempty"`
	StorageVariables    []command.StorageVariable      `json:"storage_variables,omitempty"`
	XWinterflowWarnings []string                       `json:"x_winterflow_warnings,omitempty"`
	Backups             any                            `json:"backups,omitempty"`
	BackupsError        string                         `json:"backups_error,omitempty"`
}

type sourceView struct {
	RepoURL     string `json:"repo_url"`
	Branch      string `json:"branch"`
	ComposePath string `json:"compose_path,omitempty"`
	AutoUpdate  bool   `json:"auto_update"`
	PollSeconds int    `json:"poll_seconds,omitempty"`
}

func (t *tools) getApp(ctx context.Context, in appInput) *sdk.CallToolResult {
	userID, fail := t.appTarget(ctx, in)
	if fail != nil {
		return fail
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppGet, "get app",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.GetAppObserved(ctx, userID, in.ServerID, in.AppID, o)
		})
	if fail != nil {
		return fail
	}
	return answerApp(rec)
}

// tooLargeMessage refuses an app whose app.get answer was cut to fit: an
// empty or partial view would read as the app's whole state, and a save
// built from it would remove what is missing.
const tooLargeMessage = "this app is too large to show over MCP; edit it in the Winterflow UI"

// answerApp is get_app's answer for a succeeded app.get record: never a
// view of a result that was cut to fit.
func answerApp(rec cmdresult.Record) *sdk.CallToolResult {
	got, err := decodeApp(rec.Result)
	if err != nil {
		return webmcp.ErrorText(err.Error())
	}
	return webmcp.JSONResult(viewApp(got))
}

// decodeApp reads an app.get result, refusing one that was cut to fit or
// that does not decode as an app (a rejection whose text is the answer).
func decodeApp(raw []byte) (command.GetAppResponse, error) {
	var got command.GetAppResponse
	if isTruncated(raw) {
		return got, model.Rejected(tooLargeMessage)
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.App.AppID == "" {
		return got, model.Rejected("the agent's answer did not describe the app; try again, or open it in the Winterflow UI")
	}
	return got, nil
}

// viewApp decodes app.get's byte fields; a secret's content always reads
// as the keep placeholder.
func viewApp(got command.GetAppResponse) appView {
	v := appView{
		AppID: got.App.AppID, Config: json.RawMessage("{}"),
		Files: []item{}, Variables: []item{},
		DeclaredVariables: got.DeclaredVariables, Expose: got.Expose, StorageVariables: got.StorageVariables,
		XWinterflowWarnings: got.XWinterflowWarnings, BackupsError: got.BackupsError,
	}
	if len(got.Backups) > 0 {
		v.Backups = got.Backups
	}
	if json.Valid(got.App.Config) && len(got.App.Config) > 0 {
		v.Config = got.App.Config
	}
	for _, f := range got.App.Files {
		content := string(f.Content)
		if f.Encrypted {
			content = command.EncryptedPlaceholder
		}
		v.Files = append(v.Files, item{Name: f.ItemName(), Content: content, Encrypted: f.Encrypted})
	}
	for _, it := range got.App.Variables {
		value := string(it.Content)
		if it.Encrypted {
			value = command.EncryptedPlaceholder
		}
		v.Variables = append(v.Variables, item{Name: it.ItemName(), Value: value, Encrypted: it.Encrypted})
	}
	if src := got.App.Source; src != nil {
		v.Source = &sourceView{RepoURL: src.RepoURL, Branch: src.Branch, ComposePath: src.ComposePath,
			AutoUpdate: src.AutoUpdate, PollSeconds: src.PollSeconds}
	}
	return v
}

func (t *tools) saveApp(ctx context.Context, in saveAppInput) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	if in.AppID != "" && !command.ValidAppID(in.AppID) {
		return webmcp.ErrorText(invalidAppIDMessage)
	}
	if strings.TrimSpace(in.Name) == "" {
		return webmcp.ErrorText("name is required")
	}
	start := time.Now()
	var (
		fill  map[string]string
		claim *secretreq.Claim
	)
	if in.SecretsFrom != "" {
		var err error
		fill, claim, err = t.secrets.Consume(ctx, userID, in.ServerID, in.AppID, in.SecretsFrom)
		if err != nil {
			return t.fail(err, "read the secret request")
		}
		// Every path that does not dispatch releases the claim for a
		// retry (Commit runs once: after a dispatch this is a no-op).
		defer func() {
			if err := claim.Commit(false); err != nil {
				t.Log.Error("mcp: release a secret request", "error", err, "request_id", in.SecretsFrom)
			}
		}()
		if fail := checkFill(in, fill); fail != nil {
			return fail
		}
	}
	app, payload, fail := buildSave(in, fill)
	if fail != nil {
		return fail
	}
	if fail := t.checkKeeps(ctx, userID, in, fill); fail != nil {
		return fail
	}
	secrets := secretNames(in)
	// The whole call stays within one wait: the stored-secrets read above
	// came out of it.
	bound := max(t.Wait-time.Since(start), time.Second)
	var (
		usedErr error
		// assigned is the id the dispatched save addresses (a new app's
		// is assigned by the API): every answer after a dispatch names it.
		assigned string
	)
	rec, fail := t.dispatchWithin(ctx, userID, in.ServerID, command.TypeAppSave, "save app", bound,
		func(ctx context.Context, o appuc.Observer) (string, error) {
			if claim != nil {
				// Renew the claim right before dispatching: a save whose
				// claim lapsed (and may be another's now) sends nothing.
				if err := claim.Hold(ctx); err != nil {
					return "", err
				}
			}
			appID, requestID, err := t.Apps.SaveAppAssigned(ctx, userID, in.ServerID, app, payload, in.Draft, o)
			if err != nil {
				return "", err
			}
			assigned = appID
			// Once dispatched, the request is used (its ciphertexts went to
			// the agent); a save refused before is released by the defer.
			if claim != nil {
				usedErr = claim.Commit(true)
			}
			return requestID, nil
		})
	if usedErr != nil {
		t.Log.Error("mcp: mark a secret request used", "error", usedErr, "request_id", in.SecretsFrom)
		return withAppID(webmcp.ErrorText("the save was sent, but the secret request "+in.SecretsFrom+
			" could not be marked used; do not reuse it: check the app with get_app_status, and ask again with request_secrets if needed"),
			assigned)
	}
	if fail != nil {
		return withAppID(withSecretHint(fail, secrets), assigned)
	}
	var saved command.SaveAppResponse
	_ = json.Unmarshal(rec.Result, &saved)
	appID := saved.AppID
	if appID == "" {
		appID = assigned
	}
	return withAppID(t.withStatus(rec, in.ServerID, appID), appID)
}

// withAppID adds the app's id to a dispatched save's answer (unchanged
// when nothing was dispatched): an error's text says it, a JSON answer
// (succeeded, running, unknown) carries "app_id". The agent commits a save
// before it deploys, so a create whose deploy failed or is still running
// has left the app on the server: a retry must pass this id, or it creates
// a second app.
func withAppID(res *sdk.CallToolResult, appID string) *sdk.CallToolResult {
	if appID == "" || res == nil || len(res.Content) != 1 {
		return res
	}
	text, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		return res
	}
	if res.IsError {
		return webmcp.ErrorText(text.Text + " (the save addressed app_id " + appID +
			"; pass app_id " + appID + " to save_app when you retry, or a second app is created)")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text.Text), &fields); err != nil {
		return res
	}
	id, _ := json.Marshal(appID)
	fields["app_id"] = id
	return webmcp.JSONResult(fields)
}

// checkFill refuses secrets_from values the save does not take as
// secrets: each must name a secret the save declares (x-winterflow
// variables with encrypted: true, or a variable sent with encrypted: true),
// and a variable it fills may not be sent as a plain one.
func checkFill(in saveAppInput, fill map[string]string) *sdk.CallToolResult {
	for _, v := range in.Variables {
		if _, ok := fill[strings.TrimSpace(v.Name)]; ok && !v.Encrypted {
			return webmcp.ErrorText(strings.TrimSpace(v.Name) + ": secrets_from fills it, so send it with encrypted: true (or leave it out)")
		}
	}
	secrets := secretNames(in)
	for _, name := range slices.Sorted(maps.Keys(fill)) {
		if !secrets[name] {
			return webmcp.ErrorText(name + ": secrets_from fills it, but the save does not declare it a secret: declare it in " +
				"the compose file's x-winterflow.variables with encrypted: true, or send it as a variable with encrypted: true")
		}
	}
	return nil
}

// buildSave turns save_app's input into the catalog record and the
// app.save payload the app editor would send: the config blob carries the
// files' and variables' metadata, the ingress and the git source (never a
// token); it has no ports, so the agent keeps the app's host ports and
// pins. Secret values are refused: only the keep placeholder may be sent.
// fill (secrets_from) is the ciphertexts that become secret variables'
// values: a listed one's value is replaced, the others are added.
func buildSave(in saveAppInput, fill map[string]string) (model.App, command.AppPayload, *sdk.CallToolResult) {
	type fileMeta struct {
		Filename    string `json:"filename"`
		IsEncrypted bool   `json:"is_encrypted"`
	}
	type varMeta struct {
		Name        string `json:"name"`
		IsEncrypted bool   `json:"is_encrypted"`
	}
	name := strings.TrimSpace(in.Name)
	cfg := map[string]any{"name": name, "description": in.Description, "icon": in.Icon, "color": in.Color}
	var payload command.AppPayload
	files, vars := []fileMeta{}, []varMeta{}
	for _, f := range in.Files {
		if f.Encrypted && f.Content != command.EncryptedPlaceholder {
			return model.App{}, payload, webmcp.ErrorText(plainSecretMessage)
		}
		files = append(files, fileMeta{Filename: strings.TrimSpace(f.Name), IsEncrypted: f.Encrypted})
		payload.Files = append(payload.Files, command.ContentItem{Name: strings.TrimSpace(f.Name), Content: []byte(f.Content), Encrypted: f.Encrypted})
	}
	listed := map[string]bool{}
	for _, v := range in.Variables {
		if v.Encrypted && v.Value != command.EncryptedPlaceholder {
			return model.App{}, payload, webmcp.ErrorText(plainSecretMessage)
		}
		name, value := strings.TrimSpace(v.Name), v.Value
		if ct, ok := fill[name]; ok {
			value = ct
		}
		listed[name] = true
		vars = append(vars, varMeta{Name: name, IsEncrypted: v.Encrypted})
		payload.Variables = append(payload.Variables, command.ContentItem{Name: name, Content: []byte(value), Encrypted: v.Encrypted})
	}
	for _, name := range slices.Sorted(maps.Keys(fill)) {
		if listed[name] {
			continue
		}
		vars = append(vars, varMeta{Name: name, IsEncrypted: true})
		payload.Variables = append(payload.Variables, command.ContentItem{Name: name, Content: []byte(fill[name]), Encrypted: true})
	}
	cfg["files"], cfg["variables"] = files, vars
	switch {
	case in.Ingress != nil:
		ing := model.Ingress{Domains: []model.IngressDomain{}, Redirects: []model.IngressRedirect{}}
		for _, d := range in.Ingress.Domains {
			ing.Domains = append(ing.Domains, model.IngressDomain{Domain: strings.TrimSpace(d.Domain), Expose: d.Expose, UpstreamPort: d.UpstreamPort})
		}
		for _, r := range in.Ingress.Redirects {
			ing.Redirects = append(ing.Redirects, model.IngressRedirect{Domain: strings.TrimSpace(r.Domain), Path: r.Path, To: r.To, Code: r.Code})
		}
		cfg["ingress"] = ing
	case in.AppID != "":
		// An edit without ingress removes it: the agent replaces the
		// config, so say so explicitly (as the app editor does for an
		// emptied ingress), or the save would read as leaving ingress
		// untouched and the domain index would keep the hostnames claimed.
		cfg["ingress"] = model.Ingress{Domains: []model.IngressDomain{}, Redirects: []model.IngressRedirect{}}
	}
	if src := in.Source; src != nil {
		if strings.TrimSpace(src.RepoURL) == "" || strings.TrimSpace(src.Branch) == "" {
			return model.App{}, payload, webmcp.ErrorText("source repo_url and branch are required")
		}
		payload.Source = &command.SourcePayload{
			RepoURL: strings.TrimSpace(src.RepoURL), Branch: strings.TrimSpace(src.Branch),
			ComposePath: strings.TrimSpace(src.ComposePath), AutoUpdate: src.AutoUpdate, PollSeconds: src.PollSeconds,
			// The placeholder keeps a stored token (none: anonymous).
			Token: []byte(command.EncryptedPlaceholder),
		}
		cfg["source"] = map[string]any{
			"repo_url": payload.Source.RepoURL, "branch": payload.Source.Branch, "compose_path": payload.Source.ComposePath,
			"auto_update": src.AutoUpdate, "poll_seconds": src.PollSeconds,
			// An edit may keep a stored token: say one may be set, so the
			// app editor sends the placeholder back instead of clearing it.
			"token_set": in.AppID != "",
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return model.App{}, payload, webmcp.ErrorText("internal error")
	}
	payload.Config = raw
	return model.App{ID: in.AppID, Name: name, Icon: in.Icon, Color: in.Color}, payload, nil
}

// checkKeeps refuses a "<encrypted>" that keeps nothing, as the app
// editor does (placeholderMisuse, renamedStoredSecret): the agent would drop
// it without a word and the save would succeed with the secret unset. A
// plain value may not start with the placeholder; a secret's may be only
// the placeholder (buildSave), and only for a name the agent stores a
// secret under: never on a new app, and on an edit only for the app's
// current secret variables and files (read with app.get). A variable fill
// (secrets_from) gives a value has one.
func (t *tools) checkKeeps(ctx context.Context, userID string, in saveAppInput, fill map[string]string) *sdk.CallToolResult {
	var keepVars, keepFiles []string
	for _, v := range in.Variables {
		name := strings.TrimSpace(v.Name)
		_, filled := fill[name]
		switch {
		case !v.Encrypted && strings.HasPrefix(v.Value, command.EncryptedPlaceholder):
			return webmcp.ErrorText(name + ": a plain value can't start with " + command.EncryptedPlaceholder)
		case v.Encrypted && !filled:
			keepVars = append(keepVars, name)
		}
	}
	for _, f := range in.Files {
		name := strings.TrimSpace(f.Name)
		switch {
		case !f.Encrypted && strings.HasPrefix(f.Content, command.EncryptedPlaceholder):
			return webmcp.ErrorText(name + ": a plain file can't start with " + command.EncryptedPlaceholder)
		case f.Encrypted:
			keepFiles = append(keepFiles, name)
		}
	}
	if len(keepVars)+len(keepFiles) == 0 {
		return nil
	}
	if in.AppID == "" {
		return webmcp.ErrorText(append(keepVars, keepFiles...)[0] + ": " + noStoredSecret)
	}
	vars, files, fail := t.storedSecrets(ctx, userID, in.ServerID, in.AppID)
	if fail != nil {
		return fail
	}
	for _, name := range keepVars {
		if !vars[name] {
			return webmcp.ErrorText(name + ": " + noStoredSecret)
		}
	}
	for _, name := range keepFiles {
		if !files[name] {
			return webmcp.ErrorText(name + ": " + noStoredSecret)
		}
	}
	return nil
}

// noStoredSecret refuses a keep placeholder that stands for nothing.
const noStoredSecret = "no secret is stored under this name to keep (a new app or a renamed secret has none); " +
	"use request_secrets to ask the user for it"

// storedSecrets reads which secret variables and files the agent holds for
// the app, by name.
func (t *tools) storedSecrets(ctx context.Context, userID, serverID, appID string) (vars, files map[string]bool, fail *sdk.CallToolResult) {
	got, err := t.readApp(ctx, userID, serverID, appID, "the app's stored secrets")
	if err != nil {
		return nil, nil, t.fail(err, "read the app's stored secrets")
	}
	vars, files = map[string]bool{}, map[string]bool{}
	for _, v := range got.App.Variables {
		if v.Encrypted {
			vars[v.ItemName()] = true
		}
	}
	for _, f := range got.App.Files {
		if f.Encrypted {
			files[f.ItemName()] = true
		}
	}
	return vars, files, nil
}

// readApp reads the app with app.get on the side of a tool's own command:
// the read is not recorded and takes at most a third of the wait, leaving
// the rest to the tool. what names the read in its errors, which are
// domain errors (their text is the agent's to read) unless dispatching
// failed.
func (t *tools) readApp(ctx context.Context, userID, serverID, appID, what string) (command.GetAppResponse, error) {
	results, deliver := port.NewResultChan()
	if _, err := t.Apps.GetAppObserved(ctx, userID, serverID, appID, deliver); err != nil {
		return command.GetAppResponse{}, err
	}
	res, ok := port.AwaitResult(ctx, results, t.Wait/3)
	switch {
	case !ok:
		return command.GetAppResponse{}, model.Rejected("could not read " + what + " in time; try again")
	case !res.Success:
		return command.GetAppResponse{}, model.Rejected("could not read " + what + ": " + res.Err().Error())
	}
	return decodeApp(res.Payload)
}

// secretNames are the variables a save treats as secrets: those it marks
// encrypted and those its compose files declare encrypted.
func secretNames(in saveAppInput) map[string]bool {
	out := map[string]bool{}
	for _, v := range in.Variables {
		if v.Encrypted {
			out[strings.TrimSpace(v.Name)] = true
		}
	}
	var contents [][]byte
	for _, f := range in.Files {
		if !f.Encrypted && isComposeFile(f.Name) {
			contents = append(contents, []byte(f.Content))
		}
	}
	if len(contents) == 0 {
		return out
	}
	spec, err := xwinterflow.Parse(xwinterflow.ModeApp, contents...)
	if err != nil {
		return out
	}
	for _, v := range spec.Variables {
		if v.Encrypted {
			out[v.Name] = true
		}
	}
	return out
}

// isComposeFile reports the compose file names compose looks for.
func isComposeFile(name string) bool {
	switch strings.TrimSpace(name) {
	case "compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml",
		"compose.override.yml", "compose.override.yaml", "docker-compose.override.yml", "docker-compose.override.yaml":
		return true
	}
	return false
}

// missingValueRe finds the variable a "value is required" refusal names.
var missingValueRe = regexp.MustCompile(`variable ([A-Za-z_][A-Za-z0-9_]*): a value is required`)

// withSecretHint points a refusal over a missing required secret at
// request_secrets.
func withSecretHint(res *sdk.CallToolResult, secrets map[string]bool) *sdk.CallToolResult {
	if !res.IsError || len(res.Content) != 1 {
		return res
	}
	text, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		return res
	}
	if m := missingValueRe.FindStringSubmatch(text.Text); m != nil && secrets[m[1]] {
		return webmcp.ErrorText(text.Text + "." + requestSecretsHint)
	}
	return res
}

// controlActions are control_app's actions.
var controlActions = map[string]command.ControlAction{
	"start": command.ControlStart, "stop": command.ControlStop,
	"restart": command.ControlRestart, "update": command.ControlUpdate,
}

func (t *tools) controlApp(ctx context.Context, in controlAppInput) *sdk.CallToolResult {
	userID, fail := t.appTarget(ctx, appInput{ServerID: in.ServerID, AppID: in.AppID})
	if fail != nil {
		return fail
	}
	action, ok := controlActions[in.Action]
	if !ok {
		return webmcp.ErrorText("action must be start, stop, restart or update")
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppControl, "control app",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.ControlAppObserved(ctx, userID, in.ServerID, in.AppID, action, o)
		})
	if fail != nil {
		return fail
	}
	return t.withStatus(rec, in.ServerID, in.AppID)
}

func (t *tools) renameApp(ctx context.Context, in renameAppInput) *sdk.CallToolResult {
	userID, fail := t.appTarget(ctx, appInput{ServerID: in.ServerID, AppID: in.AppID})
	if fail != nil {
		return fail
	}
	if strings.TrimSpace(in.Name) == "" {
		return webmcp.ErrorText("name is required")
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppRename, "rename app",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.RenameAppObserved(ctx, userID, in.ServerID, in.AppID, in.Name, o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}

func (t *tools) deleteApp(ctx context.Context, in deleteAppInput) *sdk.CallToolResult {
	userID, fail := t.appTarget(ctx, appInput{ServerID: in.ServerID, AppID: in.AppID})
	if fail != nil {
		return fail
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppDelete, "delete app",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.DeleteAppObserved(ctx, userID, in.ServerID, in.AppID, in.DeleteVolumes, in.DeleteBackups, o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}

func (t *tools) rollbackApp(ctx context.Context, in rollbackAppInput) *sdk.CallToolResult {
	userID, fail := t.appTarget(ctx, appInput{ServerID: in.ServerID, AppID: in.AppID})
	if fail != nil {
		return fail
	}
	if in.Revision == "" {
		return webmcp.ErrorText("revision is required")
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppRollback, "roll back app",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.RollbackAppObserved(ctx, userID, in.ServerID, in.AppID, in.Revision, o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}

func (t *tools) getAppStatus(ctx context.Context, in appStatusInput) *sdk.CallToolResult {
	if _, fail := t.owner(ctx, in.ServerID); fail != nil {
		return fail
	}
	if in.AppID != "" {
		if fail := requireAppID(in.AppID); fail != nil {
			return fail
		}
		st := t.appStatus(in.ServerID, in.AppID)
		if st == nil {
			return webmcp.JSONResult(map[string]any{"app_status": nil,
				"note": "the server's latest report has no status for this app (not deployed, or no report yet)"})
		}
		return webmcp.JSONResult(map[string]any{"app_status": st})
	}
	apps := t.Status.AppStatuses(in.ServerID, time.Now())
	if apps == nil {
		return webmcp.JSONResult(map[string]any{"apps": []command.AppStatus{},
			"note": "no recent report from this server: its agent may be offline"})
	}
	return webmcp.JSONResult(map[string]any{"apps": apps})
}

func (t *tools) getAppLogs(ctx context.Context, in logsInput) *sdk.CallToolResult {
	userID, fail := t.appTarget(ctx, appInput{ServerID: in.ServerID, AppID: in.AppID})
	if fail != nil {
		return fail
	}
	tail := in.Tail
	if tail <= 0 {
		tail = defaultLogTail
	}
	tail = min(tail, maxLogTail)
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppLogs, "get logs",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.GetLogsObserved(ctx, userID, in.ServerID, in.AppID, 0, int32(tail), o)
		})
	if fail != nil {
		return fail
	}
	if in.Service == "" {
		return result(rec)
	}
	var logs struct {
		command.GetLogsResponse
		Truncated bool `json:"truncated,omitempty"`
	}
	if err := json.Unmarshal(rec.Result, &logs); err != nil {
		return result(rec)
	}
	kept := logs.Logs[:0]
	for _, e := range logs.Logs {
		if fromService(e.Container, in.AppID, in.Service) {
			kept = append(kept, e)
		}
	}
	logs.Logs = kept
	raw, err := json.Marshal(logs)
	if err != nil {
		return result(rec)
	}
	rec.Result = raw
	return result(rec)
}

// containerIndexRe is the replica suffix compose gives container names.
var containerIndexRe = regexp.MustCompile(`-[0-9]+$`)

// fromService reports whether a compose container name
// (<project>-<service>-<n>) is one of service's in appID's project, which
// the agent names "wf-<app id>" (docker_compose.projectName).
func fromService(container, appID, service string) bool {
	return containerIndexRe.ReplaceAllString(container, "") == strings.ToLower("wf-"+appID)+"-"+service
}

func (t *tools) getAppRevisions(ctx context.Context, in appInput) *sdk.CallToolResult {
	userID, fail := t.appTarget(ctx, in)
	if fail != nil {
		return fail
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeAppRevisions, "get revisions",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.GetRevisionsObserved(ctx, userID, in.ServerID, in.AppID, o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}

func (t *tools) getImageTags(ctx context.Context, in imageInput) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	if strings.TrimSpace(in.Image) == "" {
		return webmcp.ErrorText("image is required")
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, command.TypeImageTags, "get image tags",
		func(ctx context.Context, o appuc.Observer) (string, error) {
			return t.Apps.GetImageTagsObserved(ctx, userID, in.ServerID, strings.TrimSpace(in.Image), o)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}
