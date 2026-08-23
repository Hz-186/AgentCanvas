package tool

import "context"

type DefinitionRepository interface {
	Create(ctx context.Context, item *Definition) error
	Update(ctx context.Context, item *Definition) error
	FindByID(ctx context.Context, ownerID, id int64) (*Definition, error)
	List(ctx context.Context, ownerID int64, limit, offset int) ([]Definition, error)
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type InvocationRepository interface {
	Create(ctx context.Context, item *Invocation) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]Invocation, error)
}

type PackRepository interface {
	CreatePack(ctx context.Context, item *ToolPack) error
	FindPackByID(ctx context.Context, ownerID, id int64) (*ToolPack, error)
	ListPacks(ctx context.Context, ownerID int64) ([]ToolPack, error)
	UpdatePack(ctx context.Context, item *ToolPack) error
	DeletePack(ctx context.Context, ownerID, id int64) error
	AddItem(ctx context.Context, item *ToolPackItem) error
	RemoveItem(ctx context.Context, ownerID, packID, toolID int64) error
	ListItems(ctx context.Context, ownerID, packID int64) ([]ToolPackItem, error)
	ListToolIDs(ctx context.Context, ownerID, packID int64) ([]int64, error)
}

type MCPRepository interface {
	CreateServer(ctx context.Context, item *MCPServer) error
	FindServerByID(ctx context.Context, ownerID, id int64) (*MCPServer, error)
	ListServers(ctx context.Context, ownerID int64) ([]MCPServer, error)
	UpdateServer(ctx context.Context, item *MCPServer) error
	DeleteServer(ctx context.Context, ownerID, id int64) error
	ReplaceToolCache(ctx context.Context, ownerID, serverID int64, tools []MCPToolCacheEntry) error
	ListToolCache(ctx context.Context, ownerID, serverID int64) ([]MCPToolCacheEntry, error)
}
