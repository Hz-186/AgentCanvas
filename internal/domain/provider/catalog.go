package provider

// CatalogModel 描述某个供应商预置的一个模型。
type CatalogModel struct {
	Name      string `json:"name" yaml:"name"`
	ModelType string `json:"model_type" yaml:"model_type"`
	MaxTokens int    `json:"max_tokens,omitempty" yaml:"max_tokens"`
}

// CatalogProvider 描述一个内置模型供应商及其可选模型清单。
// 它是 RagFlow llm_factories 中单个 factory 的等价物,作为"配置即数据"的单一事实来源。
type CatalogProvider struct {
	Key          string         `json:"key" yaml:"key"`
	Name         string         `json:"name" yaml:"name"`
	ProviderType string         `json:"provider_type" yaml:"provider_type"`
	BaseURL      string         `json:"base_url" yaml:"base_url"`
	DocURL       string         `json:"doc_url,omitempty" yaml:"doc_url"`
	Rank         int            `json:"rank" yaml:"rank"`
	Models       []CatalogModel `json:"models" yaml:"models"`
}

// CatalogRepository 提供对内置供应商目录的只读访问。
type CatalogRepository interface {
	List() []CatalogProvider
}
