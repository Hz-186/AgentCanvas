package reflection

const (
	RuntimeOff    = "off"
	RuntimeShadow = "shadow"
	RuntimeActive = "active"
)

type Policy struct {
	Enabled                      bool    `json:"enabled"`
	RuntimeMode                  string  `json:"runtime_mode"`
	InlineOnHardFailure          bool    `json:"inline_on_hard_failure"`
	TerminalAsync                bool    `json:"terminal_async"`
	MaxInlinePerRun              int     `json:"max_inline_per_run"`
	RecallTopK                   int     `json:"recall_top_k"`
	RecallTokenBudget            int     `json:"recall_token_budget"`
	MinImportance                float64 `json:"min_importance"`
	MinConfidence                float64 `json:"min_confidence"`
	AllowValidatedGlobalFallback bool    `json:"allow_validated_global_fallback"`
	ReflectOnSuccess             string  `json:"reflect_on_success"`
	ProviderID                   int64   `json:"provider_id,omitempty"`
	Model                        string  `json:"model,omitempty"`
}

func DefaultPolicy() Policy {
	return Policy{
		Enabled: true, RuntimeMode: RuntimeActive, InlineOnHardFailure: true, TerminalAsync: true,
		MaxInlinePerRun: 2, RecallTopK: 3, RecallTokenBudget: 800, MinImportance: .65, MinConfidence: .70,
		AllowValidatedGlobalFallback: true, ReflectOnSuccess: "external_or_novel",
	}
}

func (p Policy) Normalize() Policy {
	d := DefaultPolicy()
	if p.RuntimeMode == "" {
		p.RuntimeMode = d.RuntimeMode
	}
	if p.MaxInlinePerRun <= 0 {
		p.MaxInlinePerRun = d.MaxInlinePerRun
	}
	if p.RecallTopK <= 0 {
		p.RecallTopK = d.RecallTopK
	}
	if p.RecallTopK > 10 {
		p.RecallTopK = 10
	}
	if p.RecallTokenBudget <= 0 {
		p.RecallTokenBudget = d.RecallTokenBudget
	}
	if p.MinImportance <= 0 || p.MinImportance > 1 {
		p.MinImportance = d.MinImportance
	}
	if p.MinConfidence <= 0 || p.MinConfidence > 1 {
		p.MinConfidence = d.MinConfidence
	}
	if p.ReflectOnSuccess == "" {
		p.ReflectOnSuccess = d.ReflectOnSuccess
	}
	return p
}

func (p Policy) Active() bool { p = p.Normalize(); return p.Enabled && p.RuntimeMode != RuntimeOff }
