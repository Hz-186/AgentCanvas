package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// The fake database/sql driver below lets message repo tests exercise gorm
// without a live MySQL server: every ExecContext/QueryContext is recorded and
// canned rows are served for SELECTs, mirroring the hand-rolled fake style
// used elsewhere in this package (no external mock dependency).

type recordedStatement struct {
	Query string
	Args  []driver.Value
}

type fakeSQLDB struct {
	mu          sync.Mutex
	execs       []recordedStatement
	queries     []recordedStatement
	insertSeq   int64
	rowsForFunc func(query string) driver.Rows
}

func (f *fakeSQLDB) recordExec(query string, args []driver.Value) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, recordedStatement{Query: query, Args: args})
}

func (f *fakeSQLDB) recordQuery(query string, args []driver.Value) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, recordedStatement{Query: query, Args: args})
}

func (f *fakeSQLDB) nextInsertID() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.insertSeq++
	return f.insertSeq
}

func (f *fakeSQLDB) setRowsFor(fn func(query string) driver.Rows) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsForFunc = fn
}

func (f *fakeSQLDB) rowsFor(query string) driver.Rows {
	f.mu.Lock()
	fn := f.rowsForFunc
	f.mu.Unlock()
	if fn == nil {
		return &fakeRows{}
	}
	return fn(query)
}

func (f *fakeSQLDB) execsForTable(table string) []recordedStatement {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []recordedStatement
	for _, stmt := range f.execs {
		if strings.Contains(stmt.Query, "`"+table+"`") {
			matched = append(matched, stmt)
		}
	}
	return matched
}

type fakeDriver struct{ db *fakeSQLDB }

func (d *fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{db: d.db}, nil }

type fakeConnector struct{ db *fakeSQLDB }

func (c *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{db: c.db}, nil
}

func (c *fakeConnector) Driver() driver.Driver { return &fakeDriver{db: c.db} }

type fakeConn struct{ db *fakeSQLDB }

var (
	_ driver.Conn           = (*fakeConn)(nil)
	_ driver.ConnBeginTx    = (*fakeConn)(nil)
	_ driver.ExecerContext  = (*fakeConn)(nil)
	_ driver.QueryerContext = (*fakeConn)(nil)
)

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake driver does not support prepared statements")
}

func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return fakeTx{}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.db.recordExec(query, namedValuesToValues(args))
	return fakeResult{lastInsertID: c.db.nextInsertID(), rowsAffected: 1}, nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.db.recordQuery(query, namedValuesToValues(args))
	return c.db.rowsFor(query), nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeResult struct {
	lastInsertID int64
	rowsAffected int64
}

func (r fakeResult) LastInsertId() (int64, error) { return r.lastInsertID, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

func namedValuesToValues(args []driver.NamedValue) []driver.Value {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	return values
}

type fakeRows struct {
	columns []string
	data    [][]driver.Value
	cursor  int
}

func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error      { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.cursor >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.cursor])
	r.cursor++
	return nil
}

func newMessageRepoWithFakeDB(t *testing.T) (*MessageRepository, *fakeSQLDB) {
	t.Helper()
	fake := &fakeSQLDB{}
	sqlDB := sql.OpenDB(&fakeConnector{db: fake})
	t.Cleanup(func() { _ = sqlDB.Close() })
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return NewMessageRepository(gormDB), fake
}

// conversationAgentRows serves the agent lookup issued by Create.
func conversationAgentRows(agentID int64) func(string) driver.Rows {
	return func(query string) driver.Rows {
		if strings.Contains(query, "FROM conversations") {
			return &fakeRows{columns: []string{"agent_id"}, data: [][]driver.Value{{agentID}}}
		}
		return &fakeRows{}
	}
}

// insertColumnValues maps column name to bound argument for a recorded INSERT.
func insertColumnValues(t *testing.T, stmt recordedStatement) map[string]driver.Value {
	t.Helper()
	start := strings.Index(stmt.Query, "(")
	end := strings.Index(stmt.Query, ")")
	if start < 0 || end < start {
		t.Fatalf("unexpected insert SQL %q", stmt.Query)
	}
	columns := strings.Split(stmt.Query[start+1:end], ",")
	if len(columns) != len(stmt.Args) {
		t.Fatalf("insert SQL %q has %d columns but %d args", stmt.Query, len(columns), len(stmt.Args))
	}
	values := make(map[string]driver.Value, len(columns))
	for i, column := range columns {
		values[strings.Trim(strings.TrimSpace(column), "`")] = stmt.Args[i]
	}
	return values
}

func messageRowsFixture(rows ...[]driver.Value) func(string) driver.Rows {
	return func(query string) driver.Rows {
		if strings.Contains(query, "FROM messages") {
			return &fakeRows{
				columns: []string{"id", "owner_id", "conversation_id", "role", "content", "run_id", "token_count", "archived_at", "created_at", "content_type", "metadata_json"},
				data:    rows,
			}
		}
		return &fakeRows{}
	}
}

func TestMessageRepoCreatePersistsContentType(t *testing.T) {
	repo, fake := newMessageRepoWithFakeDB(t)
	fake.setRowsFor(conversationAgentRows(7))

	metadata := json.RawMessage(`{"tool_call_id":"call_1","tool_name":"search_web","arguments":{"query":"go gorm"}}`)
	message := &conversation.Message{
		ImmutableModel: domain.ImmutableModel{OwnerID: 1},
		ConversationID: 2,
		Role:           conversation.RoleAssistant,
		ContentType:    conversation.ContentTypeFunctionCall,
		MetadataJSON:   metadata,
		TokenCount:     5,
	}
	if err := repo.Create(context.Background(), message); err != nil {
		t.Fatal(err)
	}

	inserts := fake.execsForTable("messages")
	if len(inserts) != 1 {
		t.Fatalf("expected 1 messages insert, got %d", len(inserts))
	}
	values := insertColumnValues(t, inserts[0])
	if values["content_type"] != conversation.ContentTypeFunctionCall {
		t.Errorf("content_type = %v, want %q", values["content_type"], conversation.ContentTypeFunctionCall)
	}
	gotMetadata, _ := values["metadata_json"].([]byte)
	if !bytes.Equal(gotMetadata, metadata) {
		t.Errorf("metadata_json = %v, want %v", values["metadata_json"], metadata)
	}
	if values["role"] != conversation.RoleAssistant {
		t.Errorf("role = %v, want %q", values["role"], conversation.RoleAssistant)
	}
}

func TestMessageRepoCreateDefaultsToText(t *testing.T) {
	repo, fake := newMessageRepoWithFakeDB(t)
	fake.setRowsFor(conversationAgentRows(7))

	message := &conversation.Message{
		ImmutableModel: domain.ImmutableModel{OwnerID: 1},
		ConversationID: 2,
		Role:           conversation.RoleUser,
		Content:        "hello",
		TokenCount:     1,
	}
	if err := repo.Create(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if message.ContentType != conversation.ContentTypeText {
		t.Errorf("message.ContentType = %q, want %q", message.ContentType, conversation.ContentTypeText)
	}

	inserts := fake.execsForTable("messages")
	if len(inserts) != 1 {
		t.Fatalf("expected 1 messages insert, got %d", len(inserts))
	}
	values := insertColumnValues(t, inserts[0])
	if values["content_type"] != conversation.ContentTypeText {
		t.Errorf("content_type = %v, want %q", values["content_type"], conversation.ContentTypeText)
	}
	if values["metadata_json"] != nil {
		t.Errorf("metadata_json = %v, want nil", values["metadata_json"])
	}
}

func TestMessageRepoListReturnsTypedRows(t *testing.T) {
	repo, fake := newMessageRepoWithFakeDB(t)
	createdAt := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	metadata := []byte(`{"tool_call_id":"call_42","tool_name":"read_file","arguments":{"path":"a.go"}}`)
	fake.setRowsFor(messageRowsFixture(
		[]driver.Value{int64(101), int64(1), int64(2), conversation.RoleAssistant, "", nil, int64(3), nil, createdAt, conversation.ContentTypeFunctionCall, metadata},
		[]driver.Value{int64(102), int64(1), int64(2), conversation.RoleAssistant, "done", nil, int64(4), nil, createdAt, conversation.ContentTypeText, nil},
	))

	messages, err := repo.ListByConversation(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].ContentType != conversation.ContentTypeFunctionCall {
		t.Errorf("messages[0].ContentType = %q, want %q", messages[0].ContentType, conversation.ContentTypeFunctionCall)
	}
	toolCallID, toolName, arguments := messages[0].ToolMetadata()
	if toolCallID != "call_42" || toolName != "read_file" {
		t.Errorf("ToolMetadata() = %q, %q, want call_42, read_file", toolCallID, toolName)
	}
	if len(arguments) == 0 {
		t.Error("ToolMetadata() returned empty arguments, want parsed arguments payload")
	}
	if messages[1].ContentType != conversation.ContentTypeText {
		t.Errorf("messages[1].ContentType = %q, want %q", messages[1].ContentType, conversation.ContentTypeText)
	}
	if len(messages[1].MetadataJSON) != 0 {
		t.Errorf("messages[1].MetadataJSON = %v, want empty", messages[1].MetadataJSON)
	}
}

func TestMessageRepoCreateSkipsIndexForToolEntries(t *testing.T) {
	repo, fake := newMessageRepoWithFakeDB(t)
	fake.setRowsFor(conversationAgentRows(7))

	toolOutput := &conversation.Message{
		ImmutableModel: domain.ImmutableModel{OwnerID: 1},
		ConversationID: 2,
		Role:           conversation.RoleTool,
		Content:        `{"result":"ok"}`,
		ContentType:    conversation.ContentTypeFunctionCallOutput,
		MetadataJSON:   json.RawMessage(`{"tool_call_id":"call_1","tool_name":"search_web"}`),
		TokenCount:     2,
	}
	if err := repo.Create(context.Background(), toolOutput); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.execsForTable("context_resource_index_outbox")); got != 0 {
		t.Fatalf("index enqueue calls for function_call_output = %d, want 0", got)
	}

	textMessage := &conversation.Message{
		ImmutableModel: domain.ImmutableModel{OwnerID: 1},
		ConversationID: 2,
		Role:           conversation.RoleAssistant,
		Content:        "visible answer",
		TokenCount:     4,
	}
	if err := repo.Create(context.Background(), textMessage); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.execsForTable("context_resource_index_outbox")); got != 1 {
		t.Fatalf("index enqueue calls for text = %d, want 1", got)
	}
}

func TestMessageRepoInvalidMetadataDegradesToText(t *testing.T) {
	repo, fake := newMessageRepoWithFakeDB(t)
	createdAt := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	fake.setRowsFor(messageRowsFixture(
		[]driver.Value{int64(201), int64(1), int64(2), conversation.RoleAssistant, "fallback text", nil, int64(6), nil, createdAt, conversation.ContentTypeFunctionCall, []byte("{not-valid-json")},
	))

	messages, err := repo.ListByConversation(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("invalid metadata must not surface as an error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	toolCallID, toolName, arguments := messages[0].ToolMetadata()
	if toolCallID != "" || toolName != "" || arguments != nil {
		t.Errorf("ToolMetadata() on invalid JSON = %q, %q, %v, want zero values", toolCallID, toolName, arguments)
	}
	if messages[0].Content != "fallback text" {
		t.Errorf("content = %q, want the row to stay readable with text semantics", messages[0].Content)
	}
}

func TestMigrationMessageContentTypeUpAddsColumns(t *testing.T) {
	upSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "000012_message_content_type.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "000012_message_content_type.down.sql"))
	if err != nil {
		t.Fatal(err)
	}

	upStatements := []string{
		"ALTER TABLE messages ADD COLUMN content_type varchar(32) NOT NULL DEFAULT 'text';",
		"ALTER TABLE messages ADD COLUMN metadata_json json DEFAULT NULL;",
	}
	for _, statement := range upStatements {
		if !strings.Contains(string(upSQL), statement) {
			t.Errorf("up migration missing %q", statement)
		}
	}
	downStatements := []string{
		"ALTER TABLE messages DROP COLUMN content_type;",
		"ALTER TABLE messages DROP COLUMN metadata_json;",
	}
	for _, statement := range downStatements {
		if !strings.Contains(string(downSQL), statement) {
			t.Errorf("down migration missing %q", statement)
		}
	}
}
