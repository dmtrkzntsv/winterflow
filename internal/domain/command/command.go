// Package command defines the transport-agnostic catalog of commands that the
// API ("brain") sends to agents and the typed payloads they carry.
//
// On the wire every command travels inside a single gRPC envelope
// (proto.RequestEnvelope / proto.ResponseEnvelope): the envelope's Type field
// holds one of the Type constants below and its Payload field holds the JSON
// encoding of the matching request/response struct. The codec package
// (internal/infra/transport/codec) marshals and unmarshals between these
// structs and the envelope, keyed on Type — so handlers on both ends work with
// typed Go values instead of raw bytes, while the wire stays a single envelope.
//
// This replaces v1's ~14 hand-rolled gRPC oneof message pairs
// (winterflow-agent server.proto) with one envelope plus a type switch.
package command

// Type identifies a command. Values are stable strings shared by the API and
// the agent; they MUST NOT change once a protocol version ships.
type Type string

const (
	// App lifecycle (implemented in the first vertical slice).
	TypeAppSave    Type = "app.save"    // create/persist a new app revision
	TypeAppGet     Type = "app.get"     // fetch an app's config + revisions
	TypeAppsList   Type = "apps.list"   // list deployed apps (info) — agent is source of truth
	TypeAppsStatus Type = "apps.status" // container status for all apps

	// App lifecycle (declared now, implemented in later iterations).
	TypeAppDelete  Type = "app.delete"
	TypeAppRename  Type = "app.rename"
	TypeAppControl Type = "app.control"
	TypeAppLogs    Type = "app.logs"

	// App history (git-backed).
	TypeAppRevisions Type = "app.revisions" // list an app's commit history
	TypeAppRollback  Type = "app.rollback"  // restore a revision as a new commit + redeploy

	// Docker registries.
	TypeRegistryList   Type = "registry.list"
	TypeRegistryCreate Type = "registry.create"
	TypeRegistryDelete Type = "registry.delete"

	// Docker networks.
	TypeNetworkList   Type = "network.list"
	TypeNetworkCreate Type = "network.create"
	TypeNetworkDelete Type = "network.delete"

	// Docker volumes.
	TypeVolumeList   Type = "volume.list"
	TypeVolumeCreate Type = "volume.create"
	TypeVolumeDelete Type = "volume.delete"

	// Domain registrations (ingress names + TLS mode).
	TypeDomainList   Type = "domain.list"
	TypeDomainCreate Type = "domain.create"
	TypeDomainDelete Type = "domain.delete"

	// Shared variables (org- and server-level).
	TypeVarsSync   Type = "vars.sync"   // store the server's full shared-variables snapshot
	TypeVarsReload Type = "vars.reload" // re-render and compose up the selected apps
	// Storages (local folders and S3-compatible buckets).
	TypeStorageList   Type = "storage.list"
	TypeStorageTest   Type = "storage.test"
	TypeStorageSave   Type = "storage.save"
	TypeStorageDelete Type = "storage.delete"
	// Backups of apps that are not on this server (deleted, or another
	// server's) found in a storage: listed and deleted on request.
	TypeStorageOrphans      Type = "storage.orphans"
	TypeStorageDeleteOrphan Type = "storage.delete-orphan"

	// Backups.
	TypeBackupUpdate      Type = "backup.update"       // replace an app's backups list (overrides)
	TypeBackupPoints      Type = "backup.points"       // list a backup's restore points
	TypeBackupRun         Type = "backup.run"          // run a backup now; answers when the run ends
	TypeBackupDeletePoint Type = "backup.delete-point" // delete one restore point
	TypeBackupRestore     Type = "backup.restore"      // restore a point in place; answers when the restore ends

	// Container images.
	TypeImageTags Type = "image.tags" // list available registry tags for an image

	// Agent self-management.
	TypeAgentUpdate Type = "agent.update"
)

// Detached reports whether a command's answer waits for long work that runs
// elsewhere (backup.run and backup.restore wait for their run on the backup
// scheduler). Such a command is dispatched off the standalone bridge's single
// goroutine, so it never holds up other commands.
func (t Type) Detached() bool { return t == TypeBackupRun || t == TypeBackupRestore }

// ContentTypeJSON is the envelope content type used for every command payload.
const ContentTypeJSON = "application/json"

// SchemaVersion is the semantic version of the payload schemas in this package.
const SchemaVersion = "1.0.0"
