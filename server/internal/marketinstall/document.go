package marketinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidDocument = errors.New("marketinstall: invalid commit document")
	errPhase           = errors.New("marketinstall: step phase")
)

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

var secretKeys = map[string]struct{}{
	"env":           {},
	"customenv":     {},
	"custom_env":    {},
	"token":         {},
	"secret":        {},
	"password":      {},
	"authorization": {},
	"apikey":        {},
	"api_key":       {},
}

type Actor struct {
	WorkspaceID WorkspaceID
	MemberID    MemberID
	UserID      UserID
}

type CommitDocument struct {
	PlanRef        string
	ApprovalRef    string
	IdempotencyKey IdempotencyKey
	Plan           Plan
	Approval       Approval
	Current        CurrentFacts
	DeclaredHash   PlanHash
}

type Plan struct {
	ListingID      string
	Version        string
	PolicyVersion  string
	TargetRevision int64
	ReleaseDigest  string
	RuntimeID      string
	ModelID        string
	Agent          AgentPin
	Skills         []SkillPin
	MCPs           []MCPPin
	Connections    []ConnectionPin
	Task           *TaskPin
	Squad          *SquadPin
	Hash           PlanHash
}

type AgentPin struct {
	Name               string
	InstructionsDigest string
}

type SkillPin struct {
	Name   string
	Digest string
}

type MCPPin struct {
	Name      string
	Transport string
	URLDigest string
}

type ConnectionPin struct {
	Provider string
	Toolkit  string
	Ref      string
}

type TaskPin struct{ Name string }

type SquadPin struct{ Name string }

type Approval struct {
	Ref           string
	PlanHash      PlanHash
	PolicyVersion string
	ExpiresAt     time.Time
}

type CurrentFacts struct {
	PolicyVersion           string
	TargetRevision          int64
	ReleaseDigest           string
	ReleaseState            string
	AgentInstructionsDigest string
	Skills                  []NamedDigest
	MCPs                    []NamedDigest
}

type NamedDigest struct {
	Name   string
	Digest string
}

func ParseCommitDocument(body []byte) (CommitDocument, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return CommitDocument{}, ErrInvalidDocument
	}
	if dec.More() {
		return CommitDocument{}, ErrInvalidDocument
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return CommitDocument{}, ErrInvalidDocument
	}
	if err := walkKeys(root); err != nil {
		return CommitDocument{}, err
	}
	allowed := map[string]struct{}{
		"planRef": {}, "approvalRef": {}, "idempotencyKey": {},
		"plan": {}, "approval": {}, "current": {}, "effect": {},
	}
	for key := range obj {
		if _, ok := allowed[key]; !ok {
			return CommitDocument{}, ErrInvalidDocument
		}
	}
	effect, _ := obj["effect"].(string)
	if effect != "none" {
		return CommitDocument{}, ErrInvalidDocument
	}
	planRef, _ := obj["planRef"].(string)
	approvalRef, _ := obj["approvalRef"].(string)
	key, _ := obj["idempotencyKey"].(string)
	if planRef == "" || approvalRef == "" || !validIdempotencyKey(key) {
		return CommitDocument{}, ErrInvalidDocument
	}
	planObj, ok := obj["plan"].(map[string]any)
	if !ok {
		return CommitDocument{}, ErrInvalidDocument
	}
	hash, err := hashObject(planObj)
	if err != nil {
		return CommitDocument{}, ErrInvalidDocument
	}
	plan, err := planFrom(planObj)
	if err != nil {
		return CommitDocument{}, err
	}
	approval, err := approvalFrom(obj["approval"])
	if err != nil {
		return CommitDocument{}, err
	}
	current, err := currentFrom(obj["current"])
	if err != nil {
		return CommitDocument{}, err
	}
	if approval.Ref != approvalRef {
		return CommitDocument{}, ErrInvalidDocument
	}
	return CommitDocument{
		PlanRef:        planRef,
		ApprovalRef:    approvalRef,
		IdempotencyKey: IdempotencyKey(key),
		Plan:           plan,
		Approval:       approval,
		Current:        current,
		DeclaredHash:   hash,
	}, nil
}

func validIdempotencyKey(key string) bool {
	return len(key) >= 16 && len(key) <= 64 && idempotencyPattern.MatchString(key)
}

func hashObject(plan map[string]any) (PlanHash, error) {
	clone := make(map[string]any, len(plan))
	for key, value := range plan {
		if key == "planHash" {
			continue
		}
		clone[key] = value
	}
	raw, err := canonical(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return PlanHash("sha256:" + hex.EncodeToString(sum[:])), nil
}

// ContentDigest is sha256 of the UTF-8 bytes. That is the same digest as
// encode(sha256(convert_to(content, 'UTF8')), 'hex') on skill text.
func ContentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

type SkillFile struct {
	Path    string
	Content string
}

func SkillDigest(content string, files []SkillFile) string {
	sorted := append([]SkillFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	mac := sha256.New()
	_, _ = mac.Write([]byte(content))
	for _, file := range sorted {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(file.Path))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(file.Content))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func MCPDigest(transport, rawURL string) string {
	sum := sha256.Sum256([]byte(transport + "\n" + rawURL))
	return hex.EncodeToString(sum[:])
}

func URLHasUserinfo(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return true
	}
	return parsed.User != nil
}

func walkKeys(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, secret := secretKeys[strings.ToLower(key)]; secret {
				return ErrInvalidDocument
			}
			if err := walkKeys(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkKeys(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func planFrom(obj map[string]any) (Plan, error) {
	revision, err := intField(obj, "targetRevision")
	if err != nil {
		return Plan{}, err
	}
	agent, _ := obj["agent"].(map[string]any)
	plan := Plan{
		ListingID:      str(obj, "listingId"),
		Version:        str(obj, "version"),
		PolicyVersion:  str(obj, "policyVersion"),
		TargetRevision: revision,
		ReleaseDigest:  str(obj, "releaseDigest"),
		RuntimeID:      str(obj, "runtimeId"),
		ModelID:        str(obj, "modelId"),
		Agent: AgentPin{
			Name:               str(agent, "name"),
			InstructionsDigest: str(agent, "instructionsDigest"),
		},
		Hash: PlanHash(str(obj, "planHash")),
	}
	if plan.ListingID == "" || plan.Version == "" || plan.Agent.Name == "" || plan.Hash == "" {
		return Plan{}, ErrInvalidDocument
	}
	plan.Skills, err = skillPins(obj["skills"])
	if err != nil {
		return Plan{}, err
	}
	plan.MCPs, err = mcpPins(obj["mcps"])
	if err != nil {
		return Plan{}, err
	}
	plan.Connections, err = connectionPins(obj["connections"])
	if err != nil {
		return Plan{}, err
	}
	if task, ok := obj["task"].(map[string]any); ok {
		plan.Task = &TaskPin{Name: str(task, "name")}
	} else if _, present := obj["task"]; present && obj["task"] != nil {
		return Plan{}, ErrInvalidDocument
	}
	if squad, ok := obj["squad"].(map[string]any); ok {
		plan.Squad = &SquadPin{Name: str(squad, "name")}
	} else if _, present := obj["squad"]; present && obj["squad"] != nil {
		return Plan{}, ErrInvalidDocument
	}
	return plan, nil
}

func approvalFrom(value any) (Approval, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return Approval{}, ErrInvalidDocument
	}
	expires, _ := obj["expiresAt"].(string)
	when, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		when, err = time.Parse(time.RFC3339, expires)
		if err != nil {
			return Approval{}, ErrInvalidDocument
		}
	}
	approval := Approval{
		Ref:           str(obj, "ref"),
		PlanHash:      PlanHash(str(obj, "planHash")),
		PolicyVersion: str(obj, "policyVersion"),
		ExpiresAt:     when,
	}
	if approval.Ref == "" || approval.PlanHash == "" || approval.PolicyVersion == "" {
		return Approval{}, ErrInvalidDocument
	}
	return approval, nil
}

func currentFrom(value any) (CurrentFacts, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return CurrentFacts{}, ErrInvalidDocument
	}
	revision, err := intField(obj, "targetRevision")
	if err != nil {
		return CurrentFacts{}, err
	}
	facts := CurrentFacts{
		PolicyVersion:           str(obj, "policyVersion"),
		TargetRevision:          revision,
		ReleaseDigest:           str(obj, "releaseDigest"),
		ReleaseState:            str(obj, "releaseState"),
		AgentInstructionsDigest: str(obj, "agentInstructionsDigest"),
	}
	facts.Skills, err = namedDigests(obj["skills"])
	if err != nil {
		return CurrentFacts{}, err
	}
	facts.MCPs, err = namedDigests(obj["mcps"])
	if err != nil {
		return CurrentFacts{}, err
	}
	return facts, nil
}

func skillPins(value any) ([]SkillPin, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, ErrInvalidDocument
	}
	out := make([]SkillPin, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok || str(obj, "name") == "" || str(obj, "digest") == "" {
			return nil, ErrInvalidDocument
		}
		out = append(out, SkillPin{Name: str(obj, "name"), Digest: str(obj, "digest")})
	}
	return out, nil
}

func mcpPins(value any) ([]MCPPin, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, ErrInvalidDocument
	}
	out := make([]MCPPin, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok || str(obj, "name") == "" || str(obj, "urlDigest") == "" {
			return nil, ErrInvalidDocument
		}
		out = append(out, MCPPin{Name: str(obj, "name"), Transport: str(obj, "transport"), URLDigest: str(obj, "urlDigest")})
	}
	return out, nil
}

func connectionPins(value any) ([]ConnectionPin, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, ErrInvalidDocument
	}
	out := make([]ConnectionPin, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, ErrInvalidDocument
		}
		out = append(out, ConnectionPin{Provider: str(obj, "provider"), Toolkit: str(obj, "toolkit"), Ref: str(obj, "ref")})
	}
	return out, nil
}

func namedDigests(value any) ([]NamedDigest, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, ErrInvalidDocument
	}
	out := make([]NamedDigest, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok || str(obj, "name") == "" {
			return nil, ErrInvalidDocument
		}
		out = append(out, NamedDigest{Name: str(obj, "name"), Digest: str(obj, "digest")})
	}
	return out, nil
}

func str(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	value, _ := obj[key].(string)
	return value
}

func intField(obj map[string]any, key string) (int64, error) {
	value, ok := obj[key]
	if !ok || value == nil {
		return 0, ErrInvalidDocument
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, ErrInvalidDocument
		}
		return parsed, nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0, ErrInvalidDocument
		}
		return parsed, nil
	default:
		return 0, ErrInvalidDocument
	}
}

func canonical(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if typed {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(typed.String())
	case string:
		raw, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buf.Write(raw)
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			raw, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buf.Write(raw)
			buf.WriteByte(':')
			if err := writeCanonical(buf, typed[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		return fmt.Errorf("canonical: %T", value)
	}
	return nil
}
