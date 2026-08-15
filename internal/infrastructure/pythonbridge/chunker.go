package pythonbridge

import (
	"context"
	"fmt"

	"agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/parser"
)

type Chunker struct {
	Client *Client
	Name   string
}

// PythonChunker is the explicit name used by the bridge integration plan.
type PythonChunker = Chunker

func NewChunker(client *Client, name string) *Chunker {
	return &Chunker{Client: client, Name: name}
}

func NewPythonChunker(client *Client, name string) *PythonChunker {
	return NewChunker(client, name)
}

func (c *Chunker) Method() string {
	if c == nil {
		return ""
	}
	return c.Name
}

func (c *Chunker) ChunkDocument(ctx context.Context, doc parser.ParsedDocument, policy chunker.Policy) ([]chunker.Chunk, error) {
	if c == nil || c.Client == nil {
		return nil, fmt.Errorf("python chunker is not configured")
	}
	return c.Client.ChunkDocument(ctx, c.Name, doc, policy)
}
