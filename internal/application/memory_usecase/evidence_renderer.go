package memory_usecase

import (
	"encoding/json"
	"sort"
	"strings"

	"agentcanvas/internal/domain/conversation"
)

// EvidenceErrorState is the tri-state tool outcome carried by exchange units.
// Legacy rows without an is_error metadata key stay "unknown"; the renderer
// never infers success or failure from output text.
type EvidenceErrorState string

const (
	EvidenceErrorStateUnknown EvidenceErrorState = "unknown"
	EvidenceErrorStateSuccess EvidenceErrorState = "success"
	EvidenceErrorStateFailure EvidenceErrorState = "failure"
)

// EvidenceUnitKind distinguishes the rendered evidence shapes.
type EvidenceUnitKind string

const (
	// EvidenceUnitText is a plain user/assistant message.
	EvidenceUnitText EvidenceUnitKind = "text"
	// EvidenceUnitExchange is a function_call paired with its output by exact
	// tool_call_id.
	EvidenceUnitExchange EvidenceUnitKind = "exchange"
	// EvidenceUnitOrphanOutput is a function_call_output whose call is absent
	// from the window; it is kept rather than dropped.
	EvidenceUnitOrphanOutput EvidenceUnitKind = "orphan_output"
)

// EvidenceUnit is one rendered piece of conversation evidence. All text
// payloads (Content, Arguments, Output) are secret-redacted before entering
// the unit, so unit sequences can be fed to extraction prompts verbatim.
type EvidenceUnit struct {
	Kind      EvidenceUnitKind
	MessageID int64
	RunID     *int64
	Role      string
	// Content carries text-unit payloads.
	Content string
	// ToolCallID/ToolName/Arguments/Output carry exchange and orphan fields.
	ToolCallID string
	ToolName   string
	Arguments  string
	Output     string
	// ErrorCode and ErrorState come from the persisted is_error/error_code
	// metadata; absent keys degrade to EvidenceErrorStateUnknown.
	ErrorCode  string
	ErrorState EvidenceErrorState
	// FailureCount counts same-tool/same-arguments failures: the position in
	// the streak for failure units, and the number of failures immediately
	// preceding a recovering success. Recovered marks units whose streak ends
	// in a success with the same fingerprint.
	FailureCount int
	Recovered    bool
}

// EvidenceRenderer turns persisted message rows into ordered, redacted
// evidence units. It is pure: no repository or model access happens here.
type EvidenceRenderer struct{}

func NewEvidenceRenderer() *EvidenceRenderer { return &EvidenceRenderer{} }

// evidenceToolMetadata is the typed view over a tool row's metadata_json.
// Every parse failure degrades to zero values plus the unknown error state so
// malformed rows never panic and never masquerade as successes.
type evidenceToolMetadata struct {
	toolCallID string
	toolName   string
	arguments  string
	errorCode  string
	errorState EvidenceErrorState
}

func parseEvidenceMetadata(raw json.RawMessage) evidenceToolMetadata {
	parsed := evidenceToolMetadata{errorState: EvidenceErrorStateUnknown}
	if len(raw) == 0 {
		return parsed
	}
	var metadata struct {
		ToolCallID string          `json:"tool_call_id"`
		ToolName   string          `json:"tool_name"`
		Arguments  json.RawMessage `json:"arguments"`
		IsError    *bool           `json:"is_error"`
		ErrorCode  string          `json:"error_code"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return parsed
	}
	parsed.toolCallID = metadata.ToolCallID
	parsed.toolName = metadata.ToolName
	parsed.arguments = string(metadata.Arguments)
	parsed.errorCode = metadata.ErrorCode
	if metadata.IsError != nil {
		if *metadata.IsError {
			parsed.errorState = EvidenceErrorStateFailure
		} else {
			parsed.errorState = EvidenceErrorStateSuccess
		}
	}
	return parsed
}

type evidencePendingCall struct {
	row     conversation.Message
	meta    evidenceToolMetadata
	output  *conversation.Message
	outMeta evidenceToolMetadata
	emitted bool
}

// Render converts window rows into evidence units ascending by message id.
// Unordered input is tolerated; reasoning/system_echo rows and
// developer/system injected content are excluded; secrets are redacted before
// any payload enters a unit.
func (r *EvidenceRenderer) Render(messages []conversation.Message) []EvidenceUnit {
	sorted := append([]conversation.Message(nil), messages...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	metadataCache := make(map[int]evidenceToolMetadata, len(sorted))
	metaAt := func(index int) evidenceToolMetadata {
		if meta, ok := metadataCache[index]; ok {
			return meta
		}
		meta := parseEvidenceMetadata(sorted[index].MetadataJSON)
		metadataCache[index] = meta
		return meta
	}

	calls := make(map[string]*evidencePendingCall)
	for index := range sorted {
		row := sorted[index]
		if row.ContentType != conversation.ContentTypeFunctionCall {
			continue
		}
		meta := metaAt(index)
		if meta.toolCallID == "" {
			continue
		}
		if _, exists := calls[meta.toolCallID]; !exists {
			calls[meta.toolCallID] = &evidencePendingCall{row: row, meta: meta}
		}
	}

	orphanIndices := make(map[int]evidenceToolMetadata)
	for index := range sorted {
		row := sorted[index]
		if row.ContentType != conversation.ContentTypeFunctionCallOutput {
			continue
		}
		meta := metaAt(index)
		if meta.toolCallID != "" {
			if call := calls[meta.toolCallID]; call != nil && call.output == nil {
				output := row
				call.output = &output
				call.outMeta = meta
				continue
			}
		}
		orphanIndices[index] = meta
	}

	units := make([]EvidenceUnit, 0, len(sorted))
	for index := range sorted {
		row := sorted[index]
		if evidenceRowExcluded(row) {
			continue
		}
		switch row.ContentType {
		case conversation.ContentTypeFunctionCall:
			meta := metaAt(index)
			if meta.toolCallID == "" {
				continue
			}
			call := calls[meta.toolCallID]
			if call == nil || call.emitted || call.output == nil {
				// Calls without identity are unpairable; calls whose output
				// never reached the window carry no evidence yet.
				continue
			}
			call.emitted = true
			units = append(units, EvidenceUnit{
				Kind:       EvidenceUnitExchange,
				MessageID:  call.row.ID,
				RunID:      call.row.RunID,
				Role:       call.row.Role,
				ToolCallID: call.meta.toolCallID,
				ToolName:   call.meta.toolName,
				Arguments:  redactDurableSecrets(call.meta.arguments),
				Output:     redactDurableSecrets(strings.TrimSpace(call.output.Content)),
				ErrorCode:  call.outMeta.errorCode,
				ErrorState: call.outMeta.errorState,
			})
		case conversation.ContentTypeFunctionCallOutput:
			meta, orphan := orphanIndices[index]
			if !orphan {
				continue
			}
			units = append(units, EvidenceUnit{
				Kind:       EvidenceUnitOrphanOutput,
				MessageID:  row.ID,
				RunID:      row.RunID,
				Role:       row.Role,
				ToolCallID: meta.toolCallID,
				ToolName:   meta.toolName,
				Output:     redactDurableSecrets(strings.TrimSpace(row.Content)),
				ErrorCode:  meta.errorCode,
				ErrorState: meta.errorState,
			})
		default:
			content := strings.TrimSpace(row.Content)
			if content == "" {
				continue
			}
			units = append(units, EvidenceUnit{
				Kind:      EvidenceUnitText,
				MessageID: row.ID,
				RunID:     row.RunID,
				Role:      row.Role,
				Content:   redactDurableSecrets(content),
			})
		}
	}

	countEvidenceFailures(units)
	return units
}

// evidenceRowExcluded drops reasoning/system_echo rows and developer/system
// injected content: none of it is extractable evidence.
func evidenceRowExcluded(row conversation.Message) bool {
	switch row.ContentType {
	case conversation.ContentTypeReasoning, conversation.ContentTypeSystemEcho:
		return true
	}
	switch row.Role {
	case conversation.RoleDeveloper, conversation.RoleSystem:
		return true
	}
	return false
}

// evidenceFingerprint groups exchanges by tool and normalized arguments. The
// canonical JSON form makes the comparison insensitive to key order; rows
// with unparsable arguments fall back to the trimmed raw bytes.
func evidenceFingerprint(toolName, arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	var parsed any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err == nil {
		if canonical, err := json.Marshal(parsed); err == nil {
			trimmed = string(canonical)
		}
	}
	return toolName + "\x00" + trimmed
}

// countEvidenceFailures annotates tool units with same-tool/same-arguments
// failure streaks. A success closes the streak (Recovered on every unit in
// it) and resets the count; unknown states neither count nor reset.
func countEvidenceFailures(units []EvidenceUnit) {
	type streak struct {
		count    int
		failures []int
	}
	streaks := make(map[string]*streak)
	for index := range units {
		unit := &units[index]
		if unit.Kind != EvidenceUnitExchange && unit.Kind != EvidenceUnitOrphanOutput {
			continue
		}
		fingerprint := evidenceFingerprint(unit.ToolName, unit.Arguments)
		current := streaks[fingerprint]
		if current == nil {
			current = &streak{}
			streaks[fingerprint] = current
		}
		switch unit.ErrorState {
		case EvidenceErrorStateFailure:
			current.count++
			unit.FailureCount = current.count
			current.failures = append(current.failures, index)
		case EvidenceErrorStateSuccess:
			unit.FailureCount = current.count
			if current.count > 0 {
				unit.Recovered = true
				for _, failureIndex := range current.failures {
					units[failureIndex].Recovered = true
				}
			}
			current.count = 0
			current.failures = nil
		}
	}
}
