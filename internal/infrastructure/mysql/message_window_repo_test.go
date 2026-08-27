package mysql

import (
	"context"
	"database/sql/driver"
	"os"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// These tests pin the archive-inclusive window read used by durable memory
// extraction. The fake database/sql driver from message_repo_test.go records
// the generated SQL and serves canned rows, so window semantics are asserted
// through the exact query conditions and the rows the database would return
// for them (the established pattern in this package; a live MySQL server is
// covered by the AGENTCANVAS_TEST_MYSQL_DSN-gated integration subtest).

// windowRow builds one canned `messages` row matching the column layout of
// messageRowsFixture. A non-nil archivedAt marks a soft-archived row.
func windowRow(id, ownerID, conversationID int64, archivedAt *time.Time) []driver.Value {
	var archived driver.Value
	if archivedAt != nil {
		archived = *archivedAt
	}
	return []driver.Value{
		id, ownerID, conversationID,
		conversation.RoleUser, "body",
		nil, nil, int64(1),
		archived,
		time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
		conversation.ContentTypeText, nil,
	}
}

func archivedAt(seconds int) *time.Time {
	at := time.Date(2026, 8, 27, 12, 0, seconds, 0, time.UTC)
	return &at
}

// recordedMessagesQueries returns every recorded SELECT against `messages`
// in call order.
func recordedMessagesQueries(fake *fakeSQLDB) []recordedStatement {
	var matched []recordedStatement
	for _, stmt := range fake.queries {
		if strings.Contains(stmt.Query, "FROM `messages`") {
			matched = append(matched, stmt)
		}
	}
	return matched
}

func assertWindowQueryConditions(t *testing.T, stmt recordedStatement) {
	t.Helper()
	query := stmt.Query
	if strings.Contains(strings.ToLower(query), "archived_at") {
		t.Errorf("archive-inclusive window query must not filter archived_at, got %q", query)
	}
	if !strings.Contains(query, "owner_id = ? AND conversation_id = ?") {
		t.Errorf("window query must AND-combine owner_id and conversation_id, got %q", query)
	}
	if !strings.Contains(query, "id > ?") {
		t.Errorf("window query must exclude the afterID boundary (id > ?), got %q", query)
	}
	if !strings.Contains(query, "id <= ?") {
		t.Errorf("window query must include the throughID boundary (id <= ?), got %q", query)
	}
	if !strings.Contains(query, "ORDER BY id ASC") {
		t.Errorf("window query must order ascending by id, got %q", query)
	}
}

func assertWindowQueryArgs(t *testing.T, stmt recordedStatement, ownerID, conversationID, afterID, throughID int64) {
	t.Helper()
	want := []driver.Value{ownerID, conversationID, afterID, throughID}
	if len(stmt.Args) != len(want) {
		t.Fatalf("window query args = %v, want %v", stmt.Args, want)
	}
	for i, value := range want {
		if stmt.Args[i] != value {
			t.Errorf("window query arg[%d] = %v, want %v", i, stmt.Args[i], value)
		}
	}
}

func messageIDs(messages []conversation.Message) []int64 {
	ids := make([]int64, len(messages))
	for i, message := range messages {
		ids[i] = message.ID
	}
	return ids
}

func assertMessageIDs(t *testing.T, messages []conversation.Message, want ...int64) {
	t.Helper()
	got := messageIDs(messages)
	if len(got) != len(want) {
		t.Fatalf("message ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message ids = %v, want %v", got, want)
		}
	}
}

func TestMessageWindowRepo(t *testing.T) {
	ownerID := int64(1)
	conversationID := int64(2)

	t.Run("shouldIncludeArchivedRowsWithinWindow", func(t *testing.T) {
		repo, fake := newMessageRepoWithFakeDB(t)
		// Messages 1..10 exist; 3..7 are archived. The database answers the
		// window (2, 9] with rows 3..9 including the archived ones because the
		// query carries no archived_at filter.
		fake.setRowsFor(messageRowsFixture(
			windowRow(3, ownerID, conversationID, archivedAt(3)),
			windowRow(4, ownerID, conversationID, archivedAt(4)),
			windowRow(5, ownerID, conversationID, archivedAt(5)),
			windowRow(6, ownerID, conversationID, archivedAt(6)),
			windowRow(7, ownerID, conversationID, archivedAt(7)),
			windowRow(8, ownerID, conversationID, nil),
			windowRow(9, ownerID, conversationID, nil),
		))

		messages, err := repo.ListThroughIncludingArchived(context.Background(), ownerID, conversationID, 2, 9)
		if err != nil {
			t.Fatal(err)
		}
		assertMessageIDs(t, messages, 3, 4, 5, 6, 7, 8, 9)
		for i, message := range messages {
			wantArchived := i < 5 // rows 3..7
			if (message.ArchivedAt != nil) != wantArchived {
				t.Errorf("message %d ArchivedAt set = %v, want %v", message.ID, message.ArchivedAt != nil, wantArchived)
			}
		}

		queries := recordedMessagesQueries(fake)
		if len(queries) != 1 {
			t.Fatalf("expected 1 messages query, got %d", len(queries))
		}
		assertWindowQueryConditions(t, queries[0])
		assertWindowQueryArgs(t, queries[0], ownerID, conversationID, 2, 9)
	})

	t.Run("shouldTreatAfterExclusiveAndThroughInclusive", func(t *testing.T) {
		repo, fake := newMessageRepoWithFakeDB(t)
		// The database excludes the boundary row id=2 (id > 2) and includes the
		// boundary row id=9 (id <= 9); canned rows mirror that result set.
		fake.setRowsFor(messageRowsFixture(
			windowRow(3, ownerID, conversationID, nil),
			windowRow(9, ownerID, conversationID, nil),
		))

		messages, err := repo.ListThroughIncludingArchived(context.Background(), ownerID, conversationID, 2, 9)
		if err != nil {
			t.Fatal(err)
		}
		// Exactly {3, 9}: the afterID boundary row 2 is excluded, the throughID
		// boundary row 9 is included.
		assertMessageIDs(t, messages, 3, 9)

		queries := recordedMessagesQueries(fake)
		if len(queries) != 1 {
			t.Fatalf("expected 1 messages query, got %d", len(queries))
		}
		stmt := queries[0]
		if !strings.Contains(stmt.Query, "id > ?") || strings.Contains(stmt.Query, "id >= ?") {
			t.Errorf("afterID must be exclusive (id > ?), got %q", stmt.Query)
		}
		if !strings.Contains(stmt.Query, "id <= ?") || strings.Contains(stmt.Query, "id < ?") {
			t.Errorf("throughID must be inclusive (id <= ?), got %q", stmt.Query)
		}
		afterIndex := strings.Index(stmt.Query, "id > ?")
		throughIndex := strings.Index(stmt.Query, "id <= ?")
		if afterIndex < 0 || throughIndex < 0 || afterIndex > throughIndex {
			t.Errorf("window query must bind afterID before throughID, got %q", stmt.Query)
		}
		assertWindowQueryArgs(t, stmt, ownerID, conversationID, 2, 9)
	})

	t.Run("shouldReturnEmptyForEmptyWindow", func(t *testing.T) {
		repo, fake := newMessageRepoWithFakeDB(t)
		fake.setRowsFor(messageRowsFixture())

		messages, err := repo.ListThroughIncludingArchived(context.Background(), ownerID, conversationID, 2, 9)
		if err != nil {
			t.Fatalf("empty window must not error: %v", err)
		}
		if len(messages) != 0 {
			t.Fatalf("empty window returned %d rows, want 0", len(messages))
		}
		if queries := recordedMessagesQueries(fake); len(queries) != 1 {
			t.Fatalf("expected 1 messages query, got %d", len(queries))
		}
	})

	t.Run("shouldFilterForeignOwnerAndConversation", func(t *testing.T) {
		repo, fake := newMessageRepoWithFakeDB(t)
		// Rows of other owners/conversations inside the same id range are
		// excluded by the database because the query is scoped by owner_id AND
		// conversation_id; canned rows mirror the scoped result set.
		fake.setRowsFor(messageRowsFixture(
			windowRow(3, ownerID, conversationID, nil),
			windowRow(4, ownerID, conversationID, nil),
		))

		messages, err := repo.ListThroughIncludingArchived(context.Background(), ownerID, conversationID, 2, 9)
		if err != nil {
			t.Fatal(err)
		}
		assertMessageIDs(t, messages, 3, 4)

		queries := recordedMessagesQueries(fake)
		if len(queries) != 1 {
			t.Fatalf("expected 1 messages query, got %d", len(queries))
		}
		stmt := queries[0]
		if !strings.Contains(stmt.Query, "owner_id = ?") {
			t.Errorf("window query must filter owner_id, got %q", stmt.Query)
		}
		if !strings.Contains(stmt.Query, "conversation_id = ?") {
			t.Errorf("window query must filter conversation_id, got %q", stmt.Query)
		}
		if !strings.Contains(stmt.Query, "owner_id = ? AND conversation_id = ?") {
			t.Errorf("owner_id and conversation_id must be AND-combined, got %q", stmt.Query)
		}
		assertWindowQueryArgs(t, stmt, ownerID, conversationID, 2, 9)
	})

	t.Run("shouldReturnAscendingByID", func(t *testing.T) {
		repo, fake := newMessageRepoWithFakeDB(t)
		// Rows may be inserted out of order in the database; ORDER BY id ASC
		// makes the database serve them ascending, and the repository must
		// preserve that order untouched.
		fake.setRowsFor(messageRowsFixture(
			windowRow(5, ownerID, conversationID, nil),
			windowRow(6, ownerID, conversationID, archivedAt(6)),
			windowRow(7, ownerID, conversationID, nil),
		))

		messages, err := repo.ListThroughIncludingArchived(context.Background(), ownerID, conversationID, 2, 9)
		if err != nil {
			t.Fatal(err)
		}
		assertMessageIDs(t, messages, 5, 6, 7)
		for i := 1; i < len(messages); i++ {
			if messages[i-1].ID >= messages[i].ID {
				t.Fatalf("messages not ascending by id: %v", messageIDs(messages))
			}
		}

		queries := recordedMessagesQueries(fake)
		if len(queries) != 1 {
			t.Fatalf("expected 1 messages query, got %d", len(queries))
		}
		if !strings.Contains(queries[0].Query, "ORDER BY id ASC") {
			t.Errorf("window query must order ascending by id, got %q", queries[0].Query)
		}
	})

	t.Run("shouldLeaveActiveReadUnchanged", func(t *testing.T) {
		repo, fake := newMessageRepoWithFakeDB(t)
		// Same window as the archived fixture above, but the active reads must
		// keep their archived_at IS NULL filter: the database only serves the
		// non-archived rows 8 and 9 for them.
		fake.setRowsFor(messageRowsFixture(
			windowRow(8, ownerID, conversationID, nil),
			windowRow(9, ownerID, conversationID, nil),
		))

		afterThrough, err := repo.ListActiveAfterThrough(context.Background(), ownerID, conversationID, 2, 9)
		if err != nil {
			t.Fatal(err)
		}
		assertMessageIDs(t, afterThrough, 8, 9)

		through, err := repo.ListActiveThrough(context.Background(), ownerID, conversationID, 9)
		if err != nil {
			t.Fatal(err)
		}
		assertMessageIDs(t, through, 8, 9)

		queries := recordedMessagesQueries(fake)
		if len(queries) != 2 {
			t.Fatalf("expected 2 messages queries, got %d", len(queries))
		}
		afterThroughQuery := queries[0].Query
		if !strings.Contains(afterThroughQuery, "archived_at IS NULL") {
			t.Errorf("ListActiveAfterThrough must keep the archived_at IS NULL filter, got %q", afterThroughQuery)
		}
		if !strings.Contains(afterThroughQuery, "owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id > ? AND id <= ?") {
			t.Errorf("ListActiveAfterThrough conditions changed, got %q", afterThroughQuery)
		}
		if !strings.Contains(afterThroughQuery, "ORDER BY id ASC") {
			t.Errorf("ListActiveAfterThrough must keep ascending id order, got %q", afterThroughQuery)
		}
		assertWindowQueryArgs(t, queries[0], ownerID, conversationID, 2, 9)

		throughQuery := queries[1].Query
		if !strings.Contains(throughQuery, "archived_at IS NULL") {
			t.Errorf("ListActiveThrough must keep the archived_at IS NULL filter, got %q", throughQuery)
		}
		if !strings.Contains(throughQuery, "owner_id = ? AND conversation_id = ? AND archived_at IS NULL AND id <= ?") {
			t.Errorf("ListActiveThrough conditions changed, got %q", throughQuery)
		}
		if strings.Contains(throughQuery, "id > ?") {
			t.Errorf("ListActiveThrough must not gain a lower bound, got %q", throughQuery)
		}
		if !strings.Contains(throughQuery, "ORDER BY id ASC") {
			t.Errorf("ListActiveThrough must keep ascending id order, got %q", throughQuery)
		}
		wantThroughArgs := []driver.Value{ownerID, conversationID, int64(9)}
		if len(queries[1].Args) != len(wantThroughArgs) {
			t.Fatalf("ListActiveThrough args = %v, want %v", queries[1].Args, wantThroughArgs)
		}
		for i, value := range wantThroughArgs {
			if queries[1].Args[i] != value {
				t.Errorf("ListActiveThrough arg[%d] = %v, want %v", i, queries[1].Args[i], value)
			}
		}
	})

	t.Run("integration", func(t *testing.T) {
		dsn := os.Getenv("AGENTCANVAS_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("set AGENTCANVAS_TEST_MYSQL_DSN to run MySQL integration tests")
		}
		db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		repo := NewMessageRepository(db)
		seedOwnerID := time.Now().UnixNano()
		seedConversationID := int64(1)
		cleanup := func() {
			_ = db.Exec("DELETE FROM messages WHERE owner_id IN (?, ?)", seedOwnerID, seedOwnerID+1).Error
			_ = db.Exec("DELETE FROM context_resource_index_outbox WHERE owner_id IN (?, ?)", seedOwnerID, seedOwnerID+1).Error
		}
		cleanup()
		t.Cleanup(cleanup)

		createMessage := func(owner, conv int64, content string) conversation.Message {
			message := &conversation.Message{
				ImmutableModel: domain.ImmutableModel{OwnerID: owner},
				ConversationID: conv,
				Role:           conversation.RoleUser,
				Content:        content,
				ContentType:    conversation.ContentTypeFunctionCallOutput,
			}
			if err := repo.Create(ctx, message); err != nil {
				t.Fatalf("create %s: %v", content, err)
			}
			return *message
		}

		createMessage(seedOwnerID, seedConversationID, "m1")
		m2 := createMessage(seedOwnerID, seedConversationID, "m2")
		// Foreign rows whose ids land inside the window: another conversation
		// of the same owner and another owner's row in the same conversation.
		createMessage(seedOwnerID, seedConversationID+1, "foreign-conversation")
		createMessage(seedOwnerID+1, seedConversationID, "foreign-owner")
		m3 := createMessage(seedOwnerID, seedConversationID, "m3")
		m4 := createMessage(seedOwnerID, seedConversationID, "m4")
		m5 := createMessage(seedOwnerID, seedConversationID, "m5")
		m6 := createMessage(seedOwnerID, seedConversationID, "m6")
		m7 := createMessage(seedOwnerID, seedConversationID, "m7")
		m8 := createMessage(seedOwnerID, seedConversationID, "m8")
		m9 := createMessage(seedOwnerID, seedConversationID, "m9")
		createMessage(seedOwnerID, seedConversationID, "m10")

		if _, err := repo.ArchiveConversationMessagesThrough(ctx, seedOwnerID, seedConversationID, m7.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}

		window, err := repo.ListThroughIncludingArchived(ctx, seedOwnerID, seedConversationID, m2.ID, m9.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertMessageIDs(t, window, m3.ID, m4.ID, m5.ID, m6.ID, m7.ID, m8.ID, m9.ID)
		archivedThrough := map[int64]bool{m3.ID: true, m4.ID: true, m5.ID: true, m6.ID: true, m7.ID: true}
		for _, message := range window {
			if (message.ArchivedAt != nil) != archivedThrough[message.ID] {
				t.Errorf("message %d ArchivedAt set = %v, want %v", message.ID, message.ArchivedAt != nil, archivedThrough[message.ID])
			}
		}

		active, err := repo.ListActiveAfterThrough(ctx, seedOwnerID, seedConversationID, m2.ID, m9.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertMessageIDs(t, active, m8.ID, m9.ID)
	})
}
