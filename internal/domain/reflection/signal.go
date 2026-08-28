package reflection

import "math"

const (
	SignalToolError          = "tool_error"
	SignalToolDenied         = "tool_denied"
	SignalToolNotFound       = "tool_not_found"
	SignalRepeatedNoProgress = "repeated_no_progress"
	SignalSchemaFailure      = "schema_failure"
)

type Signal struct {
	Type             string         `json:"type"`
	StepIndex        int            `json:"step_index,omitempty"`
	Severity         float64        `json:"severity"`
	EvidenceStrength float64        `json:"evidence_strength"`
	Correctable      bool           `json:"correctable"`
	Message          string         `json:"message,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type CandidateScore struct{ Severity, EvidenceStrength, Generalizability, Confidence, Novelty float64 }

func (s CandidateScore) Importance() float64 {
	v := .30*clamp(s.Severity) + .25*clamp(s.EvidenceStrength) + .20*clamp(s.Generalizability) + .15*clamp(s.Confidence) + .10*clamp(s.Novelty)
	return math.Round(v*10000) / 10000
}

func (s CandidateScore) ShouldPersist(kind string, policy Policy) bool {
	p := policy.Normalize()
	threshold := p.MinImportance
	if kind == KindImportantStrategy && threshold < .75 {
		threshold = .75
	}
	return s.Confidence >= p.MinConfidence && s.Importance() >= threshold
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
