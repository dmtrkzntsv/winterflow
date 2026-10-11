package contract_test

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"

	appagent "winterflow/internal/app/agent"
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/codec"
	grpcagent "winterflow/internal/infra/transport/grpc/agent"
	"winterflow/internal/infra/transport/grpc/proto"
)

// reply is a scripted agent answer: Err != "" makes it an error response
// (status 1 on the SSE result), otherwise Payload is the success body.
type reply struct {
	Payload any
	Err     string
}

// script answers one command; req is the raw request payload.
type script func(req json.RawMessage) reply

// received is one command the fake agent saw.
type received struct {
	Type    command.Type
	AgentID string
	Payload json.RawMessage
}

// fakeAgent stands in for the docker-compose Dispatcher behind the real
// InProcessBridge. Every command type answers success by default with the
// same echo shape the real Dispatcher produces; tests override per type.
type fakeAgent struct {
	mu        sync.Mutex
	overrides map[command.Type]script
	calls     []received
	// domains is each agent's registry (agentID -> name -> domain): the
	// domain.* defaults read and write it, so a list after a create sees it.
	domains map[string]map[string]command.Domain
	// storages is each agent's storage registry, the same way for storage.*.
	storages map[string]map[string]command.StorageInfo
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{
		overrides: map[command.Type]script{},
		domains:   map[string]map[string]command.Domain{},
		storages:  map[string]map[string]command.StorageInfo{},
	}
}

// addStorage puts a storage straight into agentID's registry, the way one
// added before the API saw it would be.
func (a *fakeAgent) addStorage(agentID string, s command.StorageInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.storages[agentID] == nil {
		a.storages[agentID] = map[string]command.StorageInfo{}
	}
	if s.Apps == nil {
		s.Apps = []string{}
	}
	a.storages[agentID][s.Name] = s
}

// storageScript answers storage.* from agentID's registry, masking the
// secret the way the agent does.
func (a *fakeAgent) storageScript(typ command.Type, agentID string) (script, bool) {
	switch typ {
	case command.TypeStorageList:
		return func(json.RawMessage) reply {
			a.mu.Lock()
			defer a.mu.Unlock()
			out := []command.StorageInfo{}
			for _, s := range a.storages[agentID] {
				out = append(out, s)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
			return reply{Payload: command.ListStoragesResponse{Storages: out}}
		}, true
	case command.TypeStorageTest:
		return func(json.RawMessage) reply { return reply{Payload: command.TestStorageResponse{OK: true}} }, true
	case command.TypeStorageSave:
		return func(raw json.RawMessage) reply {
			req := decode[command.SaveStorageRequest](raw)
			in := req.Storage.Normalized()
			a.mu.Lock()
			prev, exists := a.storages[agentID][in.Name]
			a.mu.Unlock()
			switch {
			case req.Create && exists:
				return reply{Err: "a storage named " + in.Name + " already exists on this server"}
			case !req.Create && !exists:
				return reply{Err: "there is no storage " + in.Name + " on this server"}
			case !req.Create && prev.Type != in.Type:
				return reply{Err: "the type of an existing storage can't change — delete it and add it again"}
			}
			info := command.StorageInfo{Storage: in.Masked(), Location: in.Location(), Apps: []string{}}
			a.addStorage(agentID, info)
			return reply{Payload: command.SaveStorageResponse{Storage: info, Redeployed: []string{}}}
		}, true
	case command.TypeStorageDelete:
		return func(raw json.RawMessage) reply {
			in := decode[command.DeleteStorageRequest](raw)
			a.mu.Lock()
			delete(a.storages[agentID], in.Name)
			a.mu.Unlock()
			return reply{Payload: command.DeleteStorageResponse{Name: in.Name}}
		}, true
	}
	return nil, false
}

// register puts a domain straight into agentID's registry, the way the
// agent's upgrade path seeds one without any API call.
func (a *fakeAgent) register(agentID string, d command.Domain) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.domains[agentID] == nil {
		a.domains[agentID] = map[string]command.Domain{}
	}
	if d.Apps == nil {
		d.Apps = []string{}
	}
	a.domains[agentID][d.Name] = d
}

// domainScript answers domain.* from agentID's registry.
func (a *fakeAgent) domainScript(typ command.Type, agentID string) (script, bool) {
	switch typ {
	case command.TypeDomainList:
		return func(json.RawMessage) reply {
			a.mu.Lock()
			defer a.mu.Unlock()
			out := []command.Domain{}
			for _, d := range a.domains[agentID] {
				out = append(out, d)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
			return reply{Payload: command.ListDomainsResponse{Domains: out}}
		}, true
	case command.TypeDomainCreate:
		return func(raw json.RawMessage) reply {
			in := decode[command.CreateDomainRequest](raw)
			d := command.Domain{RegisteredDomain: model.RegisteredDomain{Name: in.Name, TLS: in.TLS}, Apps: []string{}}
			a.register(agentID, d)
			return reply{Payload: command.CreateDomainResponse{Domain: d}}
		}, true
	case command.TypeDomainDelete:
		return func(raw json.RawMessage) reply {
			in := decode[command.DeleteDomainRequest](raw)
			a.mu.Lock()
			delete(a.domains[agentID], in.Name)
			a.mu.Unlock()
			return reply{Payload: command.DeleteDomainResponse{Name: in.Name}}
		}, true
	}
	return nil, false
}

// on overrides the answer for typ for the rest of the test.
func (a *fakeAgent) on(typ command.Type, s script) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.overrides[typ] = s
}

// fail makes typ answer with an error.
func (a *fakeAgent) fail(typ command.Type, msg string) {
	a.on(typ, func(json.RawMessage) reply { return reply{Err: msg} })
}

// seen returns the commands of type typ received so far.
func (a *fakeAgent) seen(typ command.Type) []received {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []received
	for _, c := range a.calls {
		if c.Type == typ {
			out = append(out, c)
		}
	}
	return out
}

// count is the number of commands received so far, of any type.
func (a *fakeAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

// Dispatch implements agent.CommandRunner (and the gRPC transport's
// Dispatcher): decode nothing itself, answer through the real codec.
func (a *fakeAgent) Dispatch(_ context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	typ := command.Type(req.Type)
	agentID := ""
	if req.Base != nil {
		agentID = req.Base.AgentId
	}
	a.mu.Lock()
	a.calls = append(a.calls, received{Type: typ, AgentID: agentID, Payload: append(json.RawMessage(nil), req.Payload...)})
	s, ok := a.overrides[typ]
	a.mu.Unlock()
	if !ok {
		if ds, isDomain := a.domainScript(typ, agentID); isDomain {
			s = ds
		} else if ss, isStorage := a.storageScript(typ, agentID); isStorage {
			s = ss
		} else {
			s = defaultScript(typ)
		}
	}
	r := s(req.Payload)
	code, detail, body := proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", r.Payload
	if r.Err != "" {
		code, detail, body = proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, r.Err, nil
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, typ, code, detail, body)
	if err != nil {
		resp, _ = codec.EncodeResponse(agentID, req.RequestId, typ, proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, err.Error(), nil)
	}
	return resp
}

// decode is the scripts' request decoder (the real codec).
func decode[T any](raw json.RawMessage) T {
	var v T
	_ = codec.DecodePayload(raw, &v)
	return v
}

// defaultScript is the success answer for typ, echoing request fields the
// way internal/app/agent's Dispatcher does.
func defaultScript(typ command.Type) script {
	switch typ {
	case command.TypeAppSave:
		return func(raw json.RawMessage) reply {
			in := decode[command.SaveAppRequest](raw)
			return reply{Payload: command.SaveAppResponse{AppID: in.App.AppID, Revision: "rev-1"}}
		}
	case command.TypeAppsList:
		return func(json.RawMessage) reply { return reply{Payload: command.ListAppsResponse{Apps: []model.App{}}} }
	case command.TypeAppsStatus:
		return func(json.RawMessage) reply {
			return reply{Payload: command.GetAppsStatusResponse{Apps: []command.AppStatus{}}}
		}
	case command.TypeAppGet:
		return func(raw json.RawMessage) reply {
			in := decode[command.GetAppRequest](raw)
			return reply{Payload: command.GetAppResponse{App: command.AppPayload{AppID: in.AppID, Config: []byte(`{}`), Variables: []command.ContentItem{}, Files: []command.ContentItem{}}}}
		}
	case command.TypeAppControl:
		return func(raw json.RawMessage) reply {
			in := decode[command.ControlAppRequest](raw)
			return reply{Payload: command.ControlAppResponse{AppID: in.AppID, Action: in.Action}}
		}
	case command.TypeAppDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteAppResponse{AppID: decode[command.DeleteAppRequest](raw).AppID}}
		}
	case command.TypeAppRename:
		return func(raw json.RawMessage) reply {
			in := decode[command.RenameAppRequest](raw)
			return reply{Payload: command.RenameAppResponse{AppID: in.AppID, Name: in.Name}}
		}
	case command.TypeAppLogs:
		return func(raw json.RawMessage) reply {
			in := decode[command.GetLogsRequest](raw)
			return reply{Payload: command.GetLogsResponse{AppID: in.AppID, Logs: []command.LogEntry{{Timestamp: 1, Level: command.LogLevelInfo, Message: "hello"}}}}
		}
	case command.TypeAppRevisions:
		return func(raw json.RawMessage) reply {
			in := decode[command.GetRevisionsRequest](raw)
			return reply{Payload: command.GetRevisionsResponse{AppID: in.AppID, Current: "rev-1", Deployed: "rev-1",
				Revisions: []command.RevisionInfo{{Hash: "rev-1", Subject: "save", Timestamp: 1}}}}
		}
	case command.TypeAppRollback:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.RollbackAppResponse{AppID: decode[command.RollbackAppRequest](raw).AppID, Revision: "rev-2"}}
		}
	case command.TypeRegistryList:
		return func(json.RawMessage) reply {
			return reply{Payload: command.ListRegistriesResponse{Registries: []command.Registry{{Address: "ghcr.io"}}}}
		}
	case command.TypeRegistryCreate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.CreateRegistryResponse{Address: decode[command.CreateRegistryRequest](raw).Address}}
		}
	case command.TypeRegistryDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteRegistryResponse{Address: decode[command.DeleteRegistryRequest](raw).Address}}
		}
	case command.TypeNetworkList:
		return func(json.RawMessage) reply {
			return reply{Payload: command.ListNetworksResponse{Networks: []command.Network{{Name: "bridge", Driver: "bridge"}}}}
		}
	case command.TypeNetworkCreate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.CreateNetworkResponse{Name: decode[command.CreateNetworkRequest](raw).Name}}
		}
	case command.TypeNetworkDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteNetworkResponse{Name: decode[command.DeleteNetworkRequest](raw).Name}}
		}
	case command.TypeVolumeList:
		return func(json.RawMessage) reply {
			return reply{Payload: command.ListVolumesResponse{Volumes: []command.Volume{{Name: "data", Driver: "local"}}}}
		}
	case command.TypeVolumeCreate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.CreateVolumeResponse{Name: decode[command.CreateVolumeRequest](raw).Name}}
		}
	case command.TypeVolumeDelete:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.DeleteVolumeResponse{Name: decode[command.DeleteVolumeRequest](raw).Name}}
		}
	case command.TypeImageTags:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.ImageTagsResponse{Image: decode[command.ImageTagsRequest](raw).Image, Tags: []string{"latest"}}}
		}
	case command.TypeBackupUpdate:
		return func(raw json.RawMessage) reply {
			in := decode[command.UpdateBackupsRequest](raw)
			return reply{Payload: command.UpdateBackupsResponse{AppID: in.AppID, Revision: "rev-2", Backups: []recipe.EffectiveBackup{}}}
		}
	case command.TypeBackupPoints:
		return func(raw json.RawMessage) reply {
			in := decode[command.ListRestorePointsRequest](raw)
			return reply{Payload: command.ListRestorePointsResponse{AppID: in.AppID, Backup: in.Backup, Points: []command.RestorePoint{
				{ID: "20261011T120000Z-scheduled", Time: 1791720000, Kind: command.PointScheduled, Size: 1024, Expires: 1807272000}}}}
		}
	case command.TypeBackupRun:
		return func(raw json.RawMessage) reply {
			in := decode[command.RunBackupRequest](raw)
			return reply{Payload: command.RunBackupResponse{AppID: in.AppID, Backup: in.Backup,
				Point: &command.RestorePoint{ID: "20261011T130000Z-manual", Time: 1791723600, Kind: command.PointManual, Size: 2048}}}
		}
	case command.TypeBackupDeletePoint:
		return func(raw json.RawMessage) reply {
			in := decode[command.DeleteRestorePointRequest](raw)
			return reply{Payload: command.DeleteRestorePointResponse{AppID: in.AppID, Backup: in.Backup, Point: in.Point}}
		}
	case command.TypeAgentUpdate:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.UpdateAgentResponse{FromVersion: "v1", ToVersion: decode[command.UpdateAgentRequest](raw).Version, Scheduled: true}}
		}
	}
	return func(json.RawMessage) reply { return reply{Err: "fake agent: unsupported command type " + string(typ)} }
}

// TestFakeAgentAnswersEveryCommandType pins that every command type the
// API can dispatch has a default success script.
func TestFakeAgentAnswersEveryCommandType(t *testing.T) {
	t.Parallel()
	a := newFakeAgent()
	for _, typ := range []command.Type{
		command.TypeAppSave, command.TypeAppGet, command.TypeAppsList, command.TypeAppsStatus,
		command.TypeAppDelete, command.TypeAppRename, command.TypeAppControl, command.TypeAppLogs,
		command.TypeAppRevisions, command.TypeAppRollback, command.TypeRegistryList,
		command.TypeRegistryCreate, command.TypeRegistryDelete, command.TypeNetworkList,
		command.TypeNetworkCreate, command.TypeNetworkDelete, command.TypeVolumeList,
		command.TypeVolumeCreate, command.TypeVolumeDelete, command.TypeImageTags, command.TypeAgentUpdate,
		command.TypeDomainList, command.TypeDomainCreate, command.TypeDomainDelete,
		command.TypeStorageList, command.TypeStorageTest, command.TypeStorageSave, command.TypeStorageDelete,
		command.TypeBackupUpdate, command.TypeBackupPoints, command.TypeBackupRun, command.TypeBackupDeletePoint,
	} {
		payload := []byte(`{}`)
		if typ == command.TypeStorageSave {
			payload = []byte(`{"create":true}`) // an edit of a storage the fake doesn't have is refused
		}
		resp := a.Dispatch(context.Background(), &proto.RequestEnvelope{Type: string(typ), RequestId: "r", Payload: payload})
		if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
			t.Errorf("%s: default answer is %v %q", typ, resp.Base.ResponseCode, resp.Base.Detail)
		}
	}
	resp := a.Dispatch(context.Background(), &proto.RequestEnvelope{Type: "nope", RequestId: "r"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Error("unknown command type must answer an error")
	}
}

// The fake satisfies both seams the real Dispatcher fills.
var (
	_ appagent.CommandRunner = (*fakeAgent)(nil)
	_ grpcagent.Dispatcher   = (*fakeAgent)(nil)
)
