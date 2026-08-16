package vectorstore

import "testing"

func TestElasticsearchMetadataFilterUsesTermsForSlices(t *testing.T) {
	filter := metadataFilter("agent_id", []int64{0, 7})
	terms, ok := filter["terms"].(map[string]any)
	if !ok || terms["metadata.agent_id"] == nil {
		t.Fatalf("expected terms filter, got %#v", filter)
	}
	term := metadataFilter("owner_id", int64(7))
	if _, ok := term["term"]; !ok {
		t.Fatalf("expected term filter, got %#v", term)
	}
}
