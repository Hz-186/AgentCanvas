package memory

import (
	"context"
	"strings"
	"testing"

	"agentcanvas/internal/domain"
)

// citationUsageRepo is an in-memory fake for the narrow repository surface
// citation usage accounting depends on (FindByIDs + MarkUsed). It records
// every MarkUsed call so tests can assert deduplicated, owner-validated
// usage updates without a live database.
type citationUsageRepo struct {
	memories      []Memory
	findErr       error
	markUsedErr   error
	findOwnerID   int64
	findIDs       []int64
	markUsedCalls [][]int64
}

func (r *citationUsageRepo) FindByIDs(_ context.Context, ownerID int64, ids []int64) ([]Memory, error) {
	r.findOwnerID = ownerID
	r.findIDs = append([]int64(nil), ids...)
	if r.findErr != nil {
		return nil, r.findErr
	}
	return r.memories, nil
}

func (r *citationUsageRepo) MarkUsed(_ context.Context, _ int64, ids []int64) error {
	r.markUsedCalls = append(r.markUsedCalls, append([]int64(nil), ids...))
	return r.markUsedErr
}

func ownedMemory(id int64) Memory {
	return Memory{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: id, OwnerID: 1}}}
}

// MemoryCitationTest#shouldStripVisibleCitationBlock
func TestMemoryCitationShouldStripVisibleCitationBlock(t *testing.T) {
	repo := &citationUsageRepo{memories: []Memory{ownedMemory(101), ownedMemory(102)}}
	finalText := "The answer body.\n\n" +
		`<oai-mem-citation memory_id="101" source="ad_hoc">adopted detail</oai-mem-citation>` + "\n" +
		`<oai-mem-citation memory_id="102"></oai-mem-citation>` + "\n"

	outcome := AccountCitations(context.Background(), 1, finalText, repo)

	if outcome.VisibleText != "The answer body." {
		t.Fatalf("visible text = %q, want the full citation block stripped", outcome.VisibleText)
	}
	if strings.Contains(outcome.VisibleText, "oai-mem-citation") {
		t.Fatalf("visible text still contains citation markup: %q", outcome.VisibleText)
	}
	if len(outcome.Warnings) != 0 {
		t.Fatalf("valid citations produced warnings: %v", outcome.Warnings)
	}
}

// MemoryCitationTest#shouldCountValidIDsOncePerRun
func TestMemoryCitationShouldCountValidIDsOncePerRun(t *testing.T) {
	repo := &citationUsageRepo{memories: []Memory{ownedMemory(101)}}
	finalText := `<oai-mem-citation memory_id="101">first use</oai-mem-citation>` + "\n" +
		`<oai-mem-citation memory_id="101">second use</oai-mem-citation>` + "\n"

	outcome := AccountCitations(context.Background(), 1, finalText, repo)

	if len(repo.markUsedCalls) != 1 {
		t.Fatalf("MarkUsed called %d times, want exactly one bulk update per run", len(repo.markUsedCalls))
	}
	if got := repo.markUsedCalls[0]; len(got) != 1 || got[0] != 101 {
		t.Fatalf("MarkUsed ids = %v, want [101] (deduplicated)", got)
	}
	if len(outcome.UpdatedIDs) != 1 || outcome.UpdatedIDs[0] != 101 {
		t.Fatalf("updated ids = %v, want [101]", outcome.UpdatedIDs)
	}
}

// MemoryCitationTest#shouldDropMalformedLineOnly
func TestMemoryCitationShouldDropMalformedLineOnly(t *testing.T) {
	repo := &citationUsageRepo{memories: []Memory{ownedMemory(102), ownedMemory(103)}}
	finalText := `<oai-mem-citation memory_id="102">ok</oai-mem-citation>` + "\n" +
		`<oai-mem-citation memory_id=broken>no quotes</oai-mem-citation>` + "\n" +
		`<oai-mem-citation memory_id="103">ok</oai-mem-citation>` + "\n"

	outcome := AccountCitations(context.Background(), 1, finalText, repo)

	if len(outcome.Warnings) != 1 {
		t.Fatalf("warnings = %d, want exactly one for the malformed line: %v", len(outcome.Warnings), outcome.Warnings)
	}
	if len(repo.markUsedCalls) != 1 {
		t.Fatalf("MarkUsed called %d times, want one", len(repo.markUsedCalls))
	}
	if got := repo.markUsedCalls[0]; len(got) != 2 || got[0] != 102 || got[1] != 103 {
		t.Fatalf("MarkUsed ids = %v, want [102 103] (valid lines keep accounting)", got)
	}
	if outcome.VisibleText != "" {
		t.Fatalf("visible text = %q, want malformed line removed too", outcome.VisibleText)
	}
}

// MemoryCitationTest#shouldRejectForeignAndMissingIDs
func TestMemoryCitationShouldRejectForeignAndMissingIDs(t *testing.T) {
	repo := &citationUsageRepo{} // owner 1 holds neither ID 9 nor ID 10
	finalText := `<oai-mem-citation memory_id="9"></oai-mem-citation>` + "\n" +
		`<oai-mem-citation memory_id="10"></oai-mem-citation>` + "\n"

	outcome := AccountCitations(context.Background(), 1, finalText, repo)

	if len(repo.markUsedCalls) != 0 {
		t.Fatalf("MarkUsed called with %v, want zero updates for foreign/missing ids", repo.markUsedCalls)
	}
	if len(outcome.UpdatedIDs) != 0 {
		t.Fatalf("updated ids = %v, want none", outcome.UpdatedIDs)
	}
	if len(outcome.Warnings) != 2 {
		t.Fatalf("warnings = %d, want one per rejected id: %v", len(outcome.Warnings), outcome.Warnings)
	}
}

// MemoryCitationTest#shouldStripWithoutAccounting
func TestMemoryCitationShouldStripWithoutAccounting(t *testing.T) {
	raw := "Answer.\n\n" + `<oai-mem-citation memory_id="7">x</oai-mem-citation>` + "\n"
	if got := StripCitations(raw); got != "Answer." {
		t.Fatalf("StripCitations = %q, want citation block removed", got)
	}
	if got := StripCitations("no block here"); got != "no block here" {
		t.Fatalf("StripCitations on clean text = %q, want unchanged", got)
	}
}

// MemoryCitationTest#shouldNotCountReturnedUnusedMemory
func TestMemoryCitationShouldNotCountReturnedUnusedMemory(t *testing.T) {
	// The recall read returned IDs 1 and 2 (both exist for this owner), but
	// the final citation names only 1. Adoption accounting must follow the
	// citation, never the returned set.
	repo := &citationUsageRepo{memories: []Memory{ownedMemory(1), ownedMemory(2)}}
	finalText := `<oai-mem-citation memory_id="1"></oai-mem-citation>` + "\n"

	outcome := AccountCitations(context.Background(), 1, finalText, repo)

	if len(repo.markUsedCalls) != 1 {
		t.Fatalf("MarkUsed called %d times, want one", len(repo.markUsedCalls))
	}
	if got := repo.markUsedCalls[0]; len(got) != 1 || got[0] != 1 {
		t.Fatalf("MarkUsed ids = %v, want [1] (returned-but-uncited ID 2 must not count)", got)
	}
	if len(outcome.UpdatedIDs) != 1 || outcome.UpdatedIDs[0] != 1 {
		t.Fatalf("updated ids = %v, want [1]", outcome.UpdatedIDs)
	}
}
