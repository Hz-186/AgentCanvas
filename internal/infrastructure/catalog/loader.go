package catalog

import (
	"fmt"
	"io/fs"
	"sort"

	"agentcanvas/conf"
	"agentcanvas/internal/domain/provider"

	"gopkg.in/yaml.v3"
)

type Loader struct {
	providers []provider.CatalogProvider
}

func NewLoader() (*Loader, error) {
	entries, err := fs.ReadDir(conf.ProviderFiles, "providers")
	if err != nil {
		return nil, fmt.Errorf("read providers dir: %w", err)
	}

	providers := make([]provider.CatalogProvider, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(conf.ProviderFiles, "providers/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read provider file %s: %w", entry.Name(), err)
		}
		var p provider.CatalogProvider
		if err := yaml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse provider file %s: %w", entry.Name(), err)
		}
		if p.Key == "" || p.Name == "" {
			return nil, fmt.Errorf("provider file %s missing key or name", entry.Name())
		}
		providers = append(providers, p)
	}

	sort.SliceStable(providers, func(i, j int) bool {
		if providers[i].Rank != providers[j].Rank {
			return providers[i].Rank > providers[j].Rank
		}
		return providers[i].Name < providers[j].Name
	})

	return &Loader{providers: providers}, nil
}

// List 返回内置供应商目录的副本。
func (l *Loader) List() []provider.CatalogProvider {
	out := make([]provider.CatalogProvider, len(l.providers))
	copy(out, l.providers)
	return out
}
