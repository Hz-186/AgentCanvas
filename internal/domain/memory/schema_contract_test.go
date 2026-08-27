package memory

import (
	"testing"
	"time"

	"agentcanvas/internal/domain"
)

func TestMemoryUsesUsageCounters(t *testing.T) {
	now := time.Now()
	m := Memory{UsageCount: 3, LastUsedAt: &now}
	if m.UsageCount != 3 || m.LastUsedAt == nil {
		t.Fatalf("usage counters not represented on Memory")
	}
}

func TestMemoryArtifactVersioningAndKinds(t *testing.T) {
	a := MemoryArtifact{Kind: ArtifactKindSummary, Version: 2}
	if !a.ValidKind() || a.Version != 2 {
		t.Fatalf("expected versioned summary artifact")
	}
	if (MemoryArtifact{Kind: "unknown"}).ValidKind() {
		t.Fatalf("unknown artifact kind accepted")
	}
}

func TestMemoryWriteJobRejectsUnknownSource(t *testing.T) {
	if err := ValidateWriteJobSource("unknown"); err == nil {
		t.Fatalf("expected unknown source rejection")
	}
	for _, source := range WriteJobSources {
		if err := ValidateWriteJobSource(source); err != nil {
			t.Fatalf("source %q rejected: %v", source, err)
		}
	}
}

func TestMemoryWriteJobIdempotencyAndLeaseOrdering(t *testing.T) {
	now := time.Now().UTC()
	job := MemoryWriteJob{BaseModel: domain.BaseModel{OwnerID: 1}, Source: "manual", IdempotencyKey: "k", Status: WriteJobStatusPending}
	if err := job.Validate(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	if (MemoryWriteJob{BaseModel: domain.BaseModel{OwnerID: 1}, Source: "manual"}).Validate() == nil {
		t.Fatalf("missing idempotency key accepted")
	}
	if !job.CanClaimAt(now) {
		t.Fatalf("pending job should be claimable")
	}
	leaseUntil := now.Add(time.Minute)
	job.Status, job.LeaseExpiresAt = WriteJobStatusRunning, &leaseUntil
	if job.CanClaimAt(now) {
		t.Fatalf("active lease should be claimed by only one worker")
	}
}

func TestCanonicalSourceNormalizesLegacyDirectWrites(t *testing.T) {
	if got := CanonicalSource("agent_tool"); got != "manual" {
		t.Fatalf("legacy agent_tool source = %q, want manual", got)
	}
	if got := CanonicalSource("unknown"); got != "manual" {
		t.Fatalf("unknown direct source = %q, want manual", got)
	}
	if got := CanonicalSource("reflection"); got != "reflection" {
		t.Fatalf("canonical source changed: %q", got)
	}
}

func TestArtifactAndWriteJobRequireCanonicalOwnershipContracts(t *testing.T) {
	artifact := MemoryArtifact{BaseModel: domain.BaseModel{OwnerID: 1}, Kind: ArtifactKindSummary, Version: 1, Checksum: "sha256"}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}
	for _, invalid := range []MemoryArtifact{
		{BaseModel: domain.BaseModel{OwnerID: 0}, Kind: ArtifactKindSummary, Version: 1, Checksum: "sha256"},
		{BaseModel: domain.BaseModel{OwnerID: 1}, Kind: ArtifactKindSummary, Version: 1},
	} {
		if invalid.Validate() == nil {
			t.Fatalf("invalid artifact accepted: %+v", invalid)
		}
	}
	if err := (MemoryWriteJob{BaseModel: domain.BaseModel{OwnerID: 0}, Source: "manual", IdempotencyKey: "key"}).Validate(); err == nil {
		t.Fatal("ownerless write job accepted")
	}
}

func TestWriteJobLeaseEqualityIsClaimable(t *testing.T) {
	now := time.Now().UTC()
	job := MemoryWriteJob{Status: WriteJobStatusRunning, LeaseExpiresAt: &now}
	if !job.CanClaimAt(now) {
		t.Fatal("lease expiring at now must be claimable")
	}
}
