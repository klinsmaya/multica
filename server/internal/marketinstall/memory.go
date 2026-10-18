package marketinstall

import (
	"context"
	"sync"
	"time"
)

type memStore struct {
	mu    sync.Mutex
	ops   map[OperationID]Operation
	byKey map[string]OperationID
	order []OperationID
}

func newMemStore() *memStore {
	return &memStore{ops: map[OperationID]Operation{}, byKey: map[string]OperationID{}}
}

func (s *memStore) Claim(_ context.Context, row Operation) (Operation, bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(row.Actor.WorkspaceID) + "\x00" + string(row.IdempotencyKey)
	if id, ok := s.byKey[key]; ok {
		existing := s.ops[id].clone()
		if existing.PlanHash != row.PlanHash {
			return existing, false, true, nil
		}
		return existing, false, false, nil
	}
	stored := row.clone()
	s.ops[row.ID] = stored
	s.byKey[key] = row.ID
	s.order = append(s.order, row.ID)
	return stored.clone(), true, false, nil
}

func (s *memStore) Load(_ context.Context, workspace WorkspaceID, id OperationID) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.ops[id]
	if !ok || row.Actor.WorkspaceID != workspace {
		return Operation{}, errMissing
	}
	return row.clone(), nil
}

func (s *memStore) Save(_ context.Context, row Operation) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.ops[row.ID]
	if !ok || current.Actor.WorkspaceID != row.Actor.WorkspaceID {
		return Operation{}, errMissing
	}
	if current.revision != row.revision || current.leaseToken != row.leaseToken {
		return current.clone(), errRevision
	}
	next := row.clone()
	next.revision++
	s.ops[row.ID] = next
	return next.clone(), nil
}

func (s *memStore) RotateLease(_ context.Context, workspace WorkspaceID, id OperationID, token string, until, now time.Time) (Operation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.ops[id]
	if !ok || current.Actor.WorkspaceID != workspace {
		return Operation{}, false, errMissing
	}
	if current.leaseToken != "" && current.leasedUntil.After(now) {
		return current.clone(), false, nil
	}
	current.leaseToken = token
	current.leasedUntil = until
	current.revision++
	s.ops[id] = current
	return current.clone(), true, nil
}

func (s *memStore) ClaimDue(_ context.Context, now time.Time, limit int, until func(time.Time) time.Time, token func() string) ([]Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Operation
	for _, id := range s.order {
		if len(out) >= limit {
			break
		}
		row := s.ops[id]
		if !row.outboxOpen {
			continue
		}
		if row.leaseToken != "" && row.leasedUntil.After(now) {
			continue
		}
		row.leaseToken = token()
		row.leasedUntil = until(now)
		row.revision++
		s.ops[id] = row
		out = append(out, row.clone())
	}
	return out, nil
}

func (s *memStore) ListInstances(_ context.Context, workspace WorkspaceID) ([]Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Instance
	for _, id := range s.order {
		row := s.ops[id]
		if row.Actor.WorkspaceID == workspace && row.InstanceID != "" {
			out = append(out, instanceFrom(row))
		}
	}
	return out, nil
}

func (s *memStore) GetInstance(_ context.Context, workspace WorkspaceID, id InstanceID) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, opID := range s.order {
		row := s.ops[opID]
		if row.InstanceID == id && row.Actor.WorkspaceID == workspace {
			return instanceFrom(row), nil
		}
	}
	return Instance{}, errMissing
}

func instanceFrom(row Operation) Instance {
	return Instance{
		ID:        row.InstanceID,
		Workspace: row.Actor.WorkspaceID,
		ListingID: row.ListingID,
		Version:   row.Version,
		PlanHash:  row.PlanHash,
		Revision:  1,
		Fence:     row.Fence,
		Steps:     append([]Step(nil), row.Steps...),
		Bindings:  append([]Binding(nil), row.bindings...),
		CreatedAt: row.CreatedAt,
	}
}

func (o Operation) clone() Operation {
	o.Steps = append([]Step(nil), o.Steps...)
	o.bindings = append([]Binding(nil), o.bindings...)
	o.payload.Skills = append([]skillBody(nil), o.payload.Skills...)
	o.payload.MCPs = append([]mcpBody(nil), o.payload.MCPs...)
	o.payload.Toolkits = append([]string(nil), o.payload.Toolkits...)
	return o
}

type nativeFault struct {
	kind  Kind
	class string
}

type scriptedNative struct {
	mu          sync.Mutex
	agents      map[string]agentDraft
	skills      map[string]skillDraft
	mcps        map[string]mcpDraft
	skillBinds  map[string]struct{}
	mcpBinds    map[string]struct{}
	agentWrites int
	skillWrites int
	mcpWrites   int
	bindWrites  int
	taskWrites  int
	faults      []nativeFault
}

func newScriptedNative() *scriptedNative {
	return &scriptedNative{
		agents:     map[string]agentDraft{},
		skills:     map[string]skillDraft{},
		mcps:       map[string]mcpDraft{},
		skillBinds: map[string]struct{}{},
		mcpBinds:   map[string]struct{}{},
	}
}

func (n *scriptedNative) push(kind Kind, class string) {
	n.mu.Lock()
	n.faults = append(n.faults, nativeFault{kind: kind, class: class})
	n.mu.Unlock()
}

func (n *scriptedNative) pop(kind Kind) (nativeFault, bool) {
	for i, fault := range n.faults {
		if fault.kind == kind {
			n.faults = append(n.faults[:i], n.faults[i+1:]...)
			return fault, true
		}
	}
	return nativeFault{}, false
}

func (n *scriptedNative) observeAgent(_ context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	draft, ok := n.agents[nativeKey(workspace, id)]
	if !ok {
		return Presence{Definitive: true}, nil
	}
	return Presence{Found: true, Definitive: true, Digest: ContentDigest(draft.Instructions)}, nil
}

func (n *scriptedNative) observeSkill(_ context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	draft, ok := n.skills[nativeKey(workspace, id)]
	if !ok {
		return Presence{Definitive: true}, nil
	}
	return Presence{Found: true, Definitive: true, Digest: SkillDigest(draft.Content, nil)}, nil
}

func (n *scriptedNative) observeMCP(_ context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	draft, ok := n.mcps[nativeKey(workspace, id)]
	if !ok {
		return Presence{Definitive: true}, nil
	}
	return Presence{Found: true, Definitive: true, Digest: MCPDigest(draft.Transport, draft.URL)}, nil
}

func (n *scriptedNative) observeSkillBind(_ context.Context, agentID, skillID CorrelationID) (Presence, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.skillBinds[string(agentID)+"/"+string(skillID)]
	return Presence{Found: ok, Definitive: true}, nil
}

func (n *scriptedNative) observeMCPBind(_ context.Context, agentID, serverID CorrelationID) (Presence, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.mcpBinds[string(agentID)+"/"+string(serverID)]
	return Presence{Found: ok, Definitive: true}, nil
}

func (n *scriptedNative) insertAgent(_ context.Context, draft agentDraft) error {
	return n.write(KindAgent, func() {
		n.agents[nativeKey(draft.Workspace, draft.ID)] = draft
		n.agentWrites++
	})
}

func (n *scriptedNative) insertSkill(_ context.Context, draft skillDraft) error {
	return n.write(KindSkill, func() {
		n.skills[nativeKey(draft.Workspace, draft.ID)] = draft
		n.skillWrites++
	})
}

func (n *scriptedNative) insertMCP(_ context.Context, draft mcpDraft) error {
	return n.write(KindMCP, func() {
		n.mcps[nativeKey(draft.Workspace, draft.ID)] = draft
		n.mcpWrites++
	})
}

func (n *scriptedNative) bindSkill(_ context.Context, agentID, skillID CorrelationID) error {
	return n.write(KindBindSkill, func() {
		n.skillBinds[string(agentID)+"/"+string(skillID)] = struct{}{}
		n.bindWrites++
	})
}

func (n *scriptedNative) bindMCP(_ context.Context, agentID, serverID CorrelationID) error {
	return n.write(KindBindMCP, func() {
		n.mcpBinds[string(agentID)+"/"+string(serverID)] = struct{}{}
		n.bindWrites++
	})
}

func (n *scriptedNative) write(kind Kind, put func()) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	fault, ok := n.pop(kind)
	if !ok {
		put()
		return nil
	}
	switch fault.class {
	case "ambiguous-after":
		put()
		return errAmbiguous
	case "ambiguous-none":
		return errAmbiguous
	case "name":
		return errNameTaken
	case "pk":
		return errPKExists
	default:
		return errDefinite
	}
}

func nativeKey(workspace WorkspaceID, id CorrelationID) string {
	return string(workspace) + "/" + string(id)
}

func openMemory(store *memStore, natives *scriptedNative, gate Gate, catalog Catalog) *Ledger {
	return &Ledger{
		store:    store,
		natives:  natives,
		gate:     gate,
		catalog:  catalog,
		now:      func() time.Time { return time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC) },
		ids:      sequentialIDs(),
		leaseFor: 0,
	}
}

func sequentialIDs() func() string {
	n := 0
	return func() string {
		n++
		return "00000000-0000-4000-8000-" + leftPad(n)
	}
}

func leftPad(n int) string {
	const hexdigits = "0123456789abcdef"
	buf := []byte("000000000000")
	for i := len(buf) - 1; i >= 0 && n > 0; i-- {
		buf[i] = hexdigits[n%16]
		n /= 16
	}
	return string(buf)
}
