package memory_usecase

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// These tests pin the deterministic candidate quality gates (design Decision
// 7, spec "Candidates pass deterministic quality gates"): >= threshold
// semantics, finite scores within [0,1], mandatory evidence references, and
// content that survives secret redaction. Every dropped candidate records a
// reason. The gate is a pure function so Task 7's merge pass can re-gate
// merged output through the same path.

func gateValidCandidate() ExtractionCandidate {
	return ExtractionCandidate{
		Title:        "Deploy pipeline base image pin",
		Content:      "Pin the base image before deploying to avoid glibc drift.",
		Type:         "lesson",
		Confidence:   0.8,
		Importance:   0.6,
		EvidenceRefs: []string{"messages:1-2"},
	}
}

func TestCandidateGate(t *testing.T) {
	t.Run("shouldAcceptFullyValidCandidate", func(t *testing.T) {
		candidate := gateValidCandidate()
		accepted, rejections := gateExtractionCandidates([]ExtractionCandidate{candidate})
		if len(accepted) != 1 || !reflect.DeepEqual(accepted[0], candidate) {
			t.Fatalf("accepted = %+v, want the valid candidate unchanged", accepted)
		}
		if len(rejections) != 0 {
			t.Fatalf("rejections = %+v, want none", rejections)
		}
	})

	t.Run("shouldRejectBelowConfidenceOrImportance", func(t *testing.T) {
		lowConfidence := gateValidCandidate()
		lowConfidence.Confidence = 0.69
		lowImportance := gateValidCandidate()
		lowImportance.Importance = 0.49
		accepted, rejections := gateExtractionCandidates([]ExtractionCandidate{lowConfidence, lowImportance})
		if len(accepted) != 0 {
			t.Fatalf("accepted = %+v, want both sub-threshold candidates dropped", accepted)
		}
		if len(rejections) != 2 {
			t.Fatalf("rejections = %+v, want one per dropped candidate", rejections)
		}
		if !strings.Contains(rejections[0].Reason, "confidence") {
			t.Fatalf("low-confidence rejection reason = %q, want it to name the confidence gate", rejections[0].Reason)
		}
		if !strings.Contains(rejections[1].Reason, "importance") {
			t.Fatalf("low-importance rejection reason = %q, want it to name the importance gate", rejections[1].Reason)
		}
	})

	t.Run("shouldAcceptExactThresholdValues", func(t *testing.T) {
		candidate := gateValidCandidate()
		candidate.Confidence = 0.7
		candidate.Importance = 0.5
		accepted, rejections := gateExtractionCandidates([]ExtractionCandidate{candidate})
		if len(accepted) != 1 {
			t.Fatalf("accepted = %+v, want the exact-threshold candidate (>= semantics)", accepted)
		}
		if len(rejections) != 0 {
			t.Fatalf("rejections = %+v, want none at the exact thresholds", rejections)
		}
	})

	t.Run("shouldRejectBlankFieldsOrMissingEvidence", func(t *testing.T) {
		blankTitle := gateValidCandidate()
		blankTitle.Title = "   "
		blankContent := gateValidCandidate()
		blankContent.Content = ""
		noEvidence := gateValidCandidate()
		noEvidence.EvidenceRefs = nil
		emptyEvidence := gateValidCandidate()
		emptyEvidence.EvidenceRefs = []string{}
		accepted, rejections := gateExtractionCandidates([]ExtractionCandidate{blankTitle, blankContent, noEvidence, emptyEvidence})
		if len(accepted) != 0 {
			t.Fatalf("accepted = %+v, want every blank/evidence-less candidate dropped", accepted)
		}
		if len(rejections) != 4 {
			t.Fatalf("rejections = %+v, want one per dropped candidate", rejections)
		}
		if !strings.Contains(rejections[0].Reason, "title") {
			t.Fatalf("blank-title rejection reason = %q, want it to name the title gate", rejections[0].Reason)
		}
		if !strings.Contains(rejections[1].Reason, "content") {
			t.Fatalf("blank-content rejection reason = %q, want it to name the content gate", rejections[1].Reason)
		}
		if !strings.Contains(rejections[2].Reason, "evidence") {
			t.Fatalf("missing-evidence rejection reason = %q, want it to name the evidence gate", rejections[2].Reason)
		}
		if !strings.Contains(rejections[3].Reason, "evidence") {
			t.Fatalf("empty-evidence rejection reason = %q, want it to name the evidence gate", rejections[3].Reason)
		}
	})

	t.Run("shouldRejectNonFiniteOrOutOfRangeScores", func(t *testing.T) {
		notANumber := gateValidCandidate()
		notANumber.Confidence = math.NaN()
		tooHigh := gateValidCandidate()
		tooHigh.Confidence = 1.5
		tooLow := gateValidCandidate()
		tooLow.Importance = -0.1
		accepted, rejections := gateExtractionCandidates([]ExtractionCandidate{notANumber, tooHigh, tooLow})
		if len(accepted) != 0 {
			t.Fatalf("accepted = %+v, want every non-finite/out-of-range candidate dropped", accepted)
		}
		if len(rejections) != 3 {
			t.Fatalf("rejections = %+v, want one per dropped candidate", rejections)
		}
		for _, rejection := range rejections {
			if rejection.Reason == "" {
				t.Fatalf("rejection %+v carries no recorded reason", rejection)
			}
		}
	})

	t.Run("shouldRejectContentEmptyAfterRedaction", func(t *testing.T) {
		// The whole content is one secret; redaction consumes it entirely, so
		// nothing durable remains to store.
		candidate := gateValidCandidate()
		candidate.Content = "-----BEGIN RSA PRIVATE KEY-----"
		accepted, rejections := gateExtractionCandidates([]ExtractionCandidate{candidate})
		if len(accepted) != 0 {
			t.Fatalf("accepted = %+v, want the fully-redacted candidate dropped", accepted)
		}
		if len(rejections) != 1 || !strings.Contains(rejections[0].Reason, "redact") {
			t.Fatalf("rejections = %+v, want one rejection naming the redaction gate", rejections)
		}
	})
}
