package marketinstall

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/marketcatalog"
)

var (
	ErrIdempotencyConflict = errors.New("marketinstall: idempotency key reused with a different plan hash")
	ErrNotFound            = errors.New("marketinstall: not found")
	ErrFence               = errors.New("marketinstall: fence does not match")

	errAmbiguous = errors.New("marketinstall: ambiguous native write")
	errNameTaken = errors.New("marketinstall: name taken")
	errPKExists  = errors.New("marketinstall: primary key exists")
	errDefinite  = errors.New("marketinstall: native write failed")
	errRevision  = errors.New("marketinstall: revision conflict")
	errMissing   = errors.New("marketinstall: missing")
)

type Admission struct {
	Ready          bool
	Code           StaleCode
	ExistingSkills []ExistingResource
	ExistingMCPs   []ExistingResource
	RuntimeID      string
	ModelID        string
}

type ExistingResource struct {
	Name   string
	ID     CorrelationID
	Digest string
}

type Gate interface {
	Admit(ctx context.Context, caller Actor, plan Plan, listing marketcatalog.Listing) (Admission, error)
}

type Catalog interface {
	Listing(ctx context.Context, listingID, version string) (marketcatalog.Listing, error)
}

type Operation struct {
	ID             OperationID
	InstanceID     InstanceID
	IdempotencyKey IdempotencyKey
	PlanHash       PlanHash
	Actor          Actor
	Fence          FenceToken
	ApprovalRef    string
	PolicyVersion  string
	ListingID      string
	Version        string
	Steps          []Step
	CreatedAt      time.Time

	revision    int64
	outboxOpen  bool
	leaseToken  string
	leasedUntil time.Time
	payload     installPayload
	bindings    []Binding
}

func (o Operation) Status() Status { return statusOf(o.Steps) }

func (o Operation) Readiness() Readiness { return readinessOf(o.Steps) }

type Instance struct {
	ID        InstanceID
	Workspace WorkspaceID
	ListingID string
	Version   string
	PlanHash  PlanHash
	Revision  int64
	Fence     FenceToken
	Steps     []Step
	Bindings  []Binding
	CreatedAt time.Time
}

func (i Instance) Readiness() Readiness { return readinessOf(i.Steps) }

type Binding struct {
	Kind       Kind
	ResourceID CorrelationID
	Ref        string
}

type installPayload struct {
	AgentName         string      `json:"agent_name"`
	AgentDescription  string      `json:"agent_description"`
	AgentInstructions string      `json:"agent_instructions"`
	Skills            []skillBody `json:"skills"`
	MCPs              []mcpBody   `json:"mcps"`
	Toolkits          []string    `json:"toolkits"`
	RuntimeID         string      `json:"runtime_id"`
	Model             string      `json:"model"`
}

type skillBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Digest      string `json:"digest"`
}

type mcpBody struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	URL       string `json:"url"`
	Digest    string `json:"digest"`
}

type nativePort interface {
	observeAgent(ctx context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error)
	observeSkill(ctx context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error)
	observeMCP(ctx context.Context, workspace WorkspaceID, id CorrelationID) (Presence, error)
	observeSkillBind(ctx context.Context, agentID, skillID CorrelationID) (Presence, error)
	observeMCPBind(ctx context.Context, agentID, serverID CorrelationID) (Presence, error)
	insertAgent(ctx context.Context, draft agentDraft) error
	insertSkill(ctx context.Context, draft skillDraft) error
	insertMCP(ctx context.Context, draft mcpDraft) error
	bindSkill(ctx context.Context, agentID, skillID CorrelationID) error
	bindMCP(ctx context.Context, agentID, serverID CorrelationID) error
}

type agentDraft struct {
	ID           CorrelationID
	Workspace    WorkspaceID
	Owner        UserID
	Name         string
	Description  string
	Instructions string
	RuntimeID    string
	Model        string
	Toolkits     []string
}

type skillDraft struct {
	ID          CorrelationID
	Workspace   WorkspaceID
	CreatedBy   UserID
	Name        string
	Description string
	Content     string
}

type mcpDraft struct {
	ID        CorrelationID
	Workspace WorkspaceID
	CreatedBy UserID
	Name      string
	Transport string
	URL       string
}

type installStore interface {
	Claim(ctx context.Context, row Operation) (Operation, bool, bool, error)
	Load(ctx context.Context, workspace WorkspaceID, id OperationID) (Operation, error)
	Save(ctx context.Context, row Operation) (Operation, error)
	RotateLease(ctx context.Context, workspace WorkspaceID, id OperationID, token string, until, now time.Time) (Operation, bool, error)
	ClaimDue(ctx context.Context, now time.Time, limit int, until func(time.Time) time.Time, token func() string) ([]Operation, error)
	ListInstances(ctx context.Context, workspace WorkspaceID) ([]Instance, error)
	GetInstance(ctx context.Context, workspace WorkspaceID, id InstanceID) (Instance, error)
}

type Ledger struct {
	store    installStore
	natives  nativePort
	gate     Gate
	catalog  Catalog
	now      func() time.Time
	ids      func() string
	leaseFor time.Duration
}

func Open(db Beginner, gate Gate, catalog Catalog) *Ledger {
	return &Ledger{
		store:    newPGStore(db),
		natives:  newPGNatives(db),
		gate:     gate,
		catalog:  catalog,
		now:      time.Now,
		ids:      uuid.NewString,
		leaseFor: 30 * time.Second,
	}
}

func (l *Ledger) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := l.Tick(ctx); err != nil {
				slog.Warn("market install tick", "error", err)
			}
		}
	}
}

func (l *Ledger) Commit(ctx context.Context, caller Actor, doc CommitDocument) (Operation, error) {
	if doc.DeclaredHash == "" || doc.Plan.Hash != doc.DeclaredHash || doc.Approval.PlanHash != doc.DeclaredHash {
		return Operation{}, Stale{Code: CodePlanHashMismatch}
	}
	if !doc.Approval.ExpiresAt.After(l.now()) {
		return Operation{}, Stale{Code: CodeStalePolicy}
	}
	listing, err := l.catalog.Listing(ctx, doc.Plan.ListingID, doc.Plan.Version)
	if err != nil {
		return Operation{}, err
	}
	if err := matchRelease(doc, listing); err != nil {
		return Operation{}, err
	}
	admission, err := l.gate.Admit(ctx, caller, doc.Plan, listing)
	if err != nil {
		return Operation{}, err
	}
	if !admission.Ready {
		code := admission.Code
		if code == "" {
			code = CodeStaleCapabilities
		}
		return Operation{}, Stale{Code: code}
	}
	if err := matchFacts(doc, listing); err != nil {
		return Operation{}, err
	}
	steps, payload, wantInstance, err := compile(doc.Plan, admission, listing)
	if err != nil {
		return Operation{}, err
	}
	now := l.now()
	row := Operation{
		ID:             OperationID(l.ids()),
		IdempotencyKey: doc.IdempotencyKey,
		PlanHash:       doc.DeclaredHash,
		Actor:          caller,
		Fence:          FenceToken(l.ids()),
		ApprovalRef:    doc.ApprovalRef,
		PolicyVersion:  doc.Plan.PolicyVersion,
		ListingID:      doc.Plan.ListingID,
		Version:        doc.Plan.Version,
		Steps:          steps,
		CreatedAt:      now,
		revision:       1,
		outboxOpen:     !terminal(statusOf(steps)),
		leaseToken:     l.ids(),
		leasedUntil:    now.Add(l.leaseFor),
		payload:        payload,
	}
	if wantInstance {
		row.InstanceID = InstanceID(l.ids())
	}
	stored, created, conflict, err := l.store.Claim(ctx, row)
	if err != nil {
		return Operation{}, err
	}
	if conflict {
		return stored, ErrIdempotencyConflict
	}
	if !created {
		return l.driveOwned(ctx, stored)
	}
	return l.drive(ctx, stored)
}

func (l *Ledger) Get(ctx context.Context, caller Actor, id OperationID) (Operation, error) {
	row, err := l.store.Load(ctx, caller.WorkspaceID, id)
	if err != nil {
		return Operation{}, ErrNotFound
	}
	return row, nil
}

func (l *Ledger) Reconcile(ctx context.Context, caller Actor, id OperationID) (Operation, error) {
	row, err := l.store.Load(ctx, caller.WorkspaceID, id)
	if err != nil {
		return Operation{}, ErrNotFound
	}
	return l.driveOwned(ctx, row)
}

func (l *Ledger) Tick(ctx context.Context) (int, error) {
	due, err := l.store.ClaimDue(ctx, l.now(), 8, func(now time.Time) time.Time {
		return now.Add(l.leaseFor)
	}, l.ids)
	if err != nil {
		return 0, err
	}
	var failed error
	driven := 0
	for _, row := range due {
		if _, err := l.drive(ctx, row); err != nil {
			failed = err
			continue
		}
		driven++
	}
	return driven, failed
}

func (l *Ledger) ListInstances(ctx context.Context, caller Actor) ([]Instance, error) {
	return l.store.ListInstances(ctx, caller.WorkspaceID)
}

func (l *Ledger) GetInstance(ctx context.Context, caller Actor, id InstanceID) (Instance, error) {
	row, err := l.store.GetInstance(ctx, caller.WorkspaceID, id)
	if err != nil {
		return Instance{}, ErrNotFound
	}
	return row, nil
}

func (l *Ledger) driveOwned(ctx context.Context, row Operation) (Operation, error) {
	if row.leasedUntil.After(l.now()) && row.leaseToken != "" {
		return row, nil
	}
	token := l.ids()
	until := l.now().Add(l.leaseFor)
	leased, ok, err := l.store.RotateLease(ctx, row.Actor.WorkspaceID, row.ID, token, until, l.now())
	if err != nil {
		return Operation{}, err
	}
	if !ok {
		return leased, nil
	}
	return l.drive(ctx, leased)
}

func matchRelease(doc CommitDocument, listing marketcatalog.Listing) error {
	if listing.PolicyVersion == "" || doc.Current.PolicyVersion == "" || doc.Plan.PolicyVersion == "" {
		return Stale{Code: CodeStalePolicy}
	}
	if listing.PolicyVersion != doc.Plan.PolicyVersion || listing.PolicyVersion != doc.Approval.PolicyVersion || listing.PolicyVersion != doc.Current.PolicyVersion {
		return Stale{Code: CodeStalePolicy}
	}
	if listing.TargetRevision != doc.Plan.TargetRevision || listing.TargetRevision != doc.Current.TargetRevision {
		return Stale{Code: CodeStaleTargetRevision}
	}
	state := listing.ReleaseState
	if state != "published" && state != "live" {
		return Stale{Code: CodeStaleRelease}
	}
	if listing.ReleaseDigest == "" || listing.ReleaseDigest != doc.Plan.ReleaseDigest || listing.ReleaseDigest != doc.Current.ReleaseDigest {
		return Stale{Code: CodeStaleRelease}
	}
	return nil
}

func matchFacts(doc CommitDocument, listing marketcatalog.Listing) error {
	want := ContentDigest(listing.Manifest.Agent.Instructions)
	if doc.Plan.Agent.InstructionsDigest != want {
		return Stale{Code: CodeStaleRelease}
	}
	if doc.Current.AgentInstructionsDigest != "" && doc.Current.AgentInstructionsDigest != want {
		return Stale{Code: CodeStaleMapping}
	}
	if err := sameNamed(doc.Current.Skills, listingSkillDigests(listing)); err != nil {
		return err
	}
	return sameNamed(doc.Current.MCPs, listingMCPDigests(listing))
}

func sameNamed(claimed []NamedDigest, catalog map[string]string) error {
	for _, item := range claimed {
		got, ok := catalog[item.Name]
		if !ok || item.Digest != got {
			return Stale{Code: CodeStaleMapping}
		}
	}
	return nil
}

func (l *Ledger) drive(ctx context.Context, row Operation) (Operation, error) {
	if row.Fence == "" {
		return row, ErrFence
	}
	var err error
	for i := 0; i < len(row.Steps); i++ {
		row, err = l.advance(ctx, row, i)
		if err != nil {
			return row, err
		}
	}
	if terminal(row.Status()) && row.outboxOpen {
		row.outboxOpen = false
		row, err = l.store.Save(ctx, row)
	}
	return row, err
}

func (l *Ledger) advance(ctx context.Context, row Operation, i int) (Operation, error) {
	step := row.Steps[i]
	switch step.Phase {
	case PhaseApplied, PhaseRefused, PhaseFailed:
		return row, nil
	case PhaseUnknown:
		return l.persistUnknown(ctx, row, i)
	case PhaseArmed:
		return l.persistArmed(ctx, row, i)
	case PhasePending, PhaseRunning:
		switch step.Mode {
		case ModeAdopt:
			return l.persistAdopt(ctx, row, i)
		case ModeBind:
			return l.persistBind(ctx, row, i)
		case ModeWrite:
			return l.armAndInsert(ctx, row, i)
		default:
			return row, nil
		}
	default:
		return row, errPhase
	}
}

func (l *Ledger) armAndInsert(ctx context.Context, row Operation, i int) (Operation, error) {
	step := row.Steps[i]
	step.Phase = PhaseRunning
	row.Steps[i] = step
	row, err := l.store.Save(ctx, row)
	if err != nil {
		return row, err
	}
	step = row.Steps[i]
	step.Phase = PhaseArmed
	step.OutputID = CorrelationID(l.ids())
	step.Attempt = 0
	row.Steps[i] = step
	// The id is committed before the native insert. A later lookup by this id
	// tells a lost insert from an insert whose applied mark was lost.
	row, err = l.store.Save(ctx, row)
	if err != nil {
		return row, err
	}
	return l.persistArmed(ctx, row, i)
}

func (l *Ledger) persistArmed(ctx context.Context, row Operation, i int) (Operation, error) {
	step := row.Steps[i]
	saw, err := l.observe(ctx, row, step)
	if err != nil || !saw.Definitive {
		return row, nil
	}
	if saw.Found {
		return l.markApplied(ctx, row, i)
	}
	digest := catalogDigest(row.payload, step)
	allow, reason := decideInsert(step, saw, digest)
	if !allow {
		return l.markRefused(ctx, row, i, reason)
	}
	err = l.insert(ctx, row, step)
	switch {
	case err == nil:
		return l.markApplied(ctx, row, i)
	case errors.Is(err, errAmbiguous):
		step.Phase = PhaseUnknown
		row.Steps[i] = step
		return l.store.Save(ctx, row)
	case errors.Is(err, errNameTaken):
		return l.markRefused(ctx, row, i, CodeNameCollision)
	case errors.Is(err, errPKExists):
		saw, observeErr := l.observe(ctx, row, step)
		if observeErr == nil && saw.Definitive && saw.Found {
			return l.markApplied(ctx, row, i)
		}
		return l.markFailed(ctx, row, i, CodeLookupUnsupported)
	default:
		return l.markFailed(ctx, row, i, CodeNativeWrite)
	}
}

func (l *Ledger) persistUnknown(ctx context.Context, row Operation, i int) (Operation, error) {
	step := row.Steps[i]
	saw, err := l.observe(ctx, row, step)
	if err != nil {
		saw = Presence{}
	}
	next, err := afterLookup(step, saw, l.now())
	if err != nil {
		return row, err
	}
	row.Steps[i] = next
	if next.Phase == PhaseApplied {
		return l.markApplied(ctx, row, i)
	}
	return l.store.Save(ctx, row)
}

func (l *Ledger) persistAdopt(ctx context.Context, row Operation, i int) (Operation, error) {
	step := row.Steps[i]
	saw, err := l.observe(ctx, row, step)
	if err != nil || !saw.Definitive {
		return row, nil
	}
	if !saw.Found {
		return l.markRefused(ctx, row, i, RefuseReason(CodeStaleMapping))
	}
	if saw.Digest != step.Digest {
		return l.markRefused(ctx, row, i, CodeContentMismatch)
	}
	return l.markApplied(ctx, row, i)
}

func (l *Ledger) persistBind(ctx context.Context, row Operation, i int) (Operation, error) {
	step := row.Steps[i]
	parentKind := KindAgent
	childKind := KindSkill
	if step.Kind == KindBindMCP {
		childKind = KindMCP
	}
	parent, parentState := resourceStep(row.Steps, parentKind, step.ParentName)
	child, childState := resourceStep(row.Steps, childKind, step.ChildName)
	if parentState == PhaseRefused || parentState == PhaseFailed || childState == PhaseRefused || childState == PhaseFailed {
		return l.markRefused(ctx, row, i, CodeBlockedDependency)
	}
	if parentState != PhaseApplied || childState != PhaseApplied {
		return row, nil
	}
	if step.Phase != PhaseArmed {
		step.Phase = PhaseArmed
		step.Parent = parent.OutputID
		step.Child = child.OutputID
		step.Attempt = 0
		row.Steps[i] = step
		var err error
		row, err = l.store.Save(ctx, row)
		if err != nil {
			return row, err
		}
	}
	return l.persistArmed(ctx, row, i)
}

func (l *Ledger) markApplied(ctx context.Context, row Operation, i int) (Operation, error) {
	step := row.Steps[i]
	step.Phase = PhaseApplied
	step.Reason = ""
	row.Steps[i] = step
	resource := step.OutputID
	if step.Mode == ModeBind {
		resource = step.Child
	}
	if resource != "" {
		row.bindings = append(row.bindings, Binding{Kind: step.Kind, ResourceID: resource})
	}
	return l.store.Save(ctx, row)
}

func (l *Ledger) markRefused(ctx context.Context, row Operation, i int, reason RefuseReason) (Operation, error) {
	step := row.Steps[i]
	step.Phase = PhaseRefused
	step.Mode = ModeRecord
	step.Reason = reason
	row.Steps[i] = step
	return l.store.Save(ctx, row)
}

func (l *Ledger) markFailed(ctx context.Context, row Operation, i int, reason RefuseReason) (Operation, error) {
	step := row.Steps[i]
	step.Phase = PhaseFailed
	step.Mode = ModeRecord
	step.Reason = reason
	row.Steps[i] = step
	return l.store.Save(ctx, row)
}

func (l *Ledger) observe(ctx context.Context, row Operation, step Step) (Presence, error) {
	switch step.Kind {
	case KindAgent:
		return l.natives.observeAgent(ctx, row.Actor.WorkspaceID, step.OutputID)
	case KindSkill:
		return l.natives.observeSkill(ctx, row.Actor.WorkspaceID, step.OutputID)
	case KindMCP:
		return l.natives.observeMCP(ctx, row.Actor.WorkspaceID, step.OutputID)
	case KindBindSkill:
		return l.natives.observeSkillBind(ctx, step.Parent, step.Child)
	case KindBindMCP:
		return l.natives.observeMCPBind(ctx, step.Parent, step.Child)
	default:
		return Presence{}, errPhase
	}
}

func (l *Ledger) insert(ctx context.Context, row Operation, step Step) error {
	switch step.Kind {
	case KindAgent:
		return l.natives.insertAgent(ctx, agentDraft{
			ID:           step.OutputID,
			Workspace:    row.Actor.WorkspaceID,
			Owner:        row.Actor.UserID,
			Name:         row.payload.AgentName,
			Description:  row.payload.AgentDescription,
			Instructions: row.payload.AgentInstructions,
			RuntimeID:    row.payload.RuntimeID,
			Model:        row.payload.Model,
			Toolkits:     row.payload.Toolkits,
		})
	case KindSkill:
		body, ok := skillByName(row.payload, step.Name)
		if !ok {
			return errDefinite
		}
		return l.natives.insertSkill(ctx, skillDraft{
			ID:          step.OutputID,
			Workspace:   row.Actor.WorkspaceID,
			CreatedBy:   row.Actor.UserID,
			Name:        body.Name,
			Description: body.Description,
			Content:     body.Content,
		})
	case KindMCP:
		body, ok := mcpByName(row.payload, step.Name)
		if !ok || URLHasUserinfo(body.URL) {
			return errDefinite
		}
		return l.natives.insertMCP(ctx, mcpDraft{
			ID:        step.OutputID,
			Workspace: row.Actor.WorkspaceID,
			CreatedBy: row.Actor.UserID,
			Name:      body.Name,
			Transport: body.Transport,
			URL:       body.URL,
		})
	case KindBindSkill:
		return l.natives.bindSkill(ctx, step.Parent, step.Child)
	case KindBindMCP:
		return l.natives.bindMCP(ctx, step.Parent, step.Child)
	default:
		return errPhase
	}
}

func resourceStep(steps []Step, kind Kind, name string) (Step, Phase) {
	for _, step := range steps {
		if step.Kind == kind && step.Name == name {
			return step, step.Phase
		}
	}
	return Step{}, ""
}

func catalogDigest(payload installPayload, step Step) string {
	switch step.Kind {
	case KindSkill:
		if body, ok := skillByName(payload, step.Name); ok {
			return body.Digest
		}
	case KindMCP:
		if body, ok := mcpByName(payload, step.Name); ok {
			return body.Digest
		}
	default:
		return step.Digest
	}
	return ""
}

func skillByName(payload installPayload, name string) (skillBody, bool) {
	for _, body := range payload.Skills {
		if body.Name == name {
			return body, true
		}
	}
	return skillBody{}, false
}

func mcpByName(payload installPayload, name string) (mcpBody, bool) {
	for _, body := range payload.MCPs {
		if body.Name == name {
			return body, true
		}
	}
	return mcpBody{}, false
}
