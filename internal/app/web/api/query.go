package api

// ServerQuery is the query of every route addressed to one server: the
// handler 400s without server_id and 403s unless the caller's org owns it.
type ServerQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Target server id."`
}
