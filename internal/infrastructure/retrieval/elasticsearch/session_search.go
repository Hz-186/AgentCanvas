package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"

	esclient "github.com/elastic/go-elasticsearch/v8"
)

const defaultMessageIndex = "agentcanvas_messages_v1"

const messageIndexMapping = `{
  "mappings": {
    "dynamic": "strict",
    "properties": {
      "owner_id": {"type": "long"},
      "agent_id": {"type": "long"},
      "conversation_id": {"type": "long"},
      "message_id": {"type": "long"},
      "role": {"type": "keyword"},
      "content": {"type": "text"},
      "created_at": {"type": "date"}
    }
  }
}`

type SessionSearchStore struct {
	client *esclient.Client
	index  string
}

func NewSessionSearchStore(client *esclient.Client, index string) *SessionSearchStore {
	if strings.TrimSpace(index) == "" {
		index = defaultMessageIndex
	}
	return &SessionSearchStore{client: client, index: strings.TrimSpace(index)}
}

func (s *SessionSearchStore) EnsureIndex(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("session search client is not configured")
	}
	response, err := s.client.Indices.Exists([]string{s.index}, s.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		return nil
	}
	if response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("check message index failed: %s", response.Status())
	}
	response, err = s.client.Indices.Create(s.index, s.client.Indices.Create.WithContext(ctx), s.client.Indices.Create.WithBody(strings.NewReader(messageIndexMapping)))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.IsError() {
		return responseError("create message index", response)
	}
	return nil
}

func (s *SessionSearchStore) IndexMessage(ctx context.Context, ownerID, agentID int64, message *conversation.Message) error {
	if message == nil || ownerID <= 0 || agentID <= 0 || message.ID <= 0 {
		return fmt.Errorf("message search document is invalid")
	}
	document, err := json.Marshal(map[string]any{"owner_id": ownerID, "agent_id": agentID, "conversation_id": message.ConversationID,
		"message_id": message.ID, "role": message.Role, "content": message.Content, "created_at": message.CreatedAt})
	if err != nil {
		return err
	}
	response, err := s.client.Index(s.index, bytes.NewReader(document), s.client.Index.WithContext(ctx),
		s.client.Index.WithDocumentID(strconv.FormatInt(message.ID, 10)), s.client.Index.WithRefresh("false"))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.IsError() {
		return responseError("index session message", response)
	}
	return nil
}

func (s *SessionSearchStore) SearchMessages(ctx context.Context, request conversation.MessageSearchRequest) ([]conversation.MessageSearchResult, error) {
	if request.OwnerID <= 0 || request.AgentID <= 0 || strings.TrimSpace(request.Query) == "" {
		return nil, fmt.Errorf("owner_id, agent_id, and query are required")
	}
	if request.Limit <= 0 || request.Limit > 50 {
		request.Limit = 10
	}
	filters := []map[string]any{{"term": map[string]any{"owner_id": request.OwnerID}}, {"term": map[string]any{"agent_id": request.AgentID}}}
	if request.ConversationID != nil {
		filters = append(filters, map[string]any{"term": map[string]any{"conversation_id": *request.ConversationID}})
	}
	if request.From != nil || request.To != nil {
		rangeQuery := map[string]any{}
		if request.From != nil {
			rangeQuery["gte"] = request.From.UTC()
		}
		if request.To != nil {
			rangeQuery["lte"] = request.To.UTC()
		}
		filters = append(filters, map[string]any{"range": map[string]any{"created_at": rangeQuery}})
	}
	body, _ := json.Marshal(map[string]any{"size": request.Limit, "query": map[string]any{"bool": map[string]any{
		"filter": filters, "must": []map[string]any{{"simple_query_string": map[string]any{"query": request.Query, "fields": []string{"content"}, "default_operator": "and"}}},
	}}, "sort": []map[string]any{{"_score": map[string]any{"order": "desc"}}, {"created_at": map[string]any{"order": "desc"}}}})
	response, err := s.client.Search(s.client.Search.WithContext(ctx), s.client.Search.WithIndex(s.index), s.client.Search.WithBody(bytes.NewReader(body)))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.IsError() {
		return nil, responseError("search session messages", response)
	}
	var decoded struct {
		Hits struct {
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					AgentID        int64     `json:"agent_id"`
					ConversationID int64     `json:"conversation_id"`
					MessageID      int64     `json:"message_id"`
					Role           string    `json:"role"`
					Content        string    `json:"content"`
					CreatedAt      time.Time `json:"created_at"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	results := make([]conversation.MessageSearchResult, 0, len(decoded.Hits.Hits))
	for _, hit := range decoded.Hits.Hits {
		results = append(results, conversation.MessageSearchResult{MessageID: hit.Source.MessageID, AgentID: hit.Source.AgentID,
			ConversationID: hit.Source.ConversationID, Role: hit.Source.Role, Content: hit.Source.Content, Score: hit.Score, CreatedAt: hit.Source.CreatedAt})
	}
	return results, nil
}

func (s *SessionSearchStore) DeleteConversation(ctx context.Context, ownerID, agentID, conversationID int64) error {
	body, _ := json.Marshal(map[string]any{"query": map[string]any{"bool": map[string]any{"filter": []map[string]any{
		{"term": map[string]any{"owner_id": ownerID}}, {"term": map[string]any{"agent_id": agentID}}, {"term": map[string]any{"conversation_id": conversationID}},
	}}}})
	response, err := s.client.DeleteByQuery([]string{s.index}, bytes.NewReader(body), s.client.DeleteByQuery.WithContext(ctx), s.client.DeleteByQuery.WithRefresh(true), s.client.DeleteByQuery.WithConflicts("proceed"))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.IsError() {
		return responseError("delete session messages", response)
	}
	return nil
}

var _ conversation.MessageSearchIndex = (*SessionSearchStore)(nil)
