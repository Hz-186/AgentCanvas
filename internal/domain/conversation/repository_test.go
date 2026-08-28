package conversation

import (
	"context"
	"testing"
)

// windowReadStub is a minimal in-memory MessageRepository used to pin the
// interface contract. Calling the archive-inclusive window read through the
// MessageRepository interface variable keeps the method part of the published
// contract: removing it from the interface breaks compilation here.
type windowReadStub struct {
	rows          []Message
	lastOwnerID   int64
	lastAfterID   int64
	lastThroughID int64
}

func (s *windowReadStub) Create(context.Context, *Message) error { return nil }

func (s *windowReadStub) ListByConversation(context.Context, int64, int64) ([]Message, error) {
	return nil, nil
}

func (s *windowReadStub) ListActiveByConversation(context.Context, int64, int64) ([]Message, error) {
	return nil, nil
}

func (s *windowReadStub) ListThroughIncludingArchived(_ context.Context, ownerID, _ int64, afterID, throughID int64) ([]Message, error) {
	s.lastOwnerID = ownerID
	s.lastAfterID = afterID
	s.lastThroughID = throughID
	return s.rows, nil
}

func TestMessageRepositoryContractIncludesArchiveInclusiveWindowRead(t *testing.T) {
	var repo MessageRepository = &windowReadStub{rows: []Message{{ConversationID: 2, Role: RoleUser}}}

	messages, err := repo.ListThroughIncludingArchived(context.Background(), 1, 2, 2, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ConversationID != 2 {
		t.Fatalf("window read returned %+v, want the stubbed row", messages)
	}
	stub := repo.(*windowReadStub)
	if stub.lastOwnerID != 1 || stub.lastAfterID != 2 || stub.lastThroughID != 9 {
		t.Fatalf("window read arguments = owner:%d after:%d through:%d, want 1, 2, 9", stub.lastOwnerID, stub.lastAfterID, stub.lastThroughID)
	}
}
