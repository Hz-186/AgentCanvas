package mysql

import (
	"testing"

	"agentcanvas/internal/domain/conversation"
)

func TestNormalizeMessageDefaultsEmptyMetadataJSON(t *testing.T) {
	message := &conversation.Message{}

	normalizeMessage(message)

	if message.MetadataJSON != "{}" {
		t.Fatalf("expected empty metadata json to default to {}, got %q", message.MetadataJSON)
	}
}

func TestNormalizeMessageKeepsExistingMetadataJSON(t *testing.T) {
	message := &conversation.Message{MetadataJSON: `{"source":"test"}`}

	normalizeMessage(message)

	if message.MetadataJSON != `{"source":"test"}` {
		t.Fatalf("expected existing metadata json to be preserved, got %q", message.MetadataJSON)
	}
}

func TestNormalizeMessageReferenceDefaultsEmptyMetadataJSON(t *testing.T) {
	ref := &conversation.MessageReference{}

	normalizeMessageReference(ref)

	if ref.MetadataJSON != "{}" {
		t.Fatalf("expected empty reference metadata json to default to {}, got %q", ref.MetadataJSON)
	}
}

func TestNormalizeMessageReferenceKeepsExistingMetadataJSON(t *testing.T) {
	ref := &conversation.MessageReference{MetadataJSON: `{"chunk":"kept"}`}

	normalizeMessageReference(ref)

	if ref.MetadataJSON != `{"chunk":"kept"}` {
		t.Fatalf("expected existing reference metadata json to be preserved, got %q", ref.MetadataJSON)
	}
}
