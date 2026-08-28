package llm

import (
	"strings"
	"testing"
)

func TestProposedPlanStreamParserSupportsChunkedExclusiveLines(t *testing.T) {
	parser := &ProposedPlanStreamParser{}
	var visibleBuf strings.Builder
	var planBuf strings.Builder
	collect := func(events []ModelStreamEvent) {
		for _, event := range events {
			switch event.Kind {
			case ModelTextDelta:
				visibleBuf.WriteString(event.Text)
			case ModelProposedPlanDelta:
				planBuf.WriteString(event.Text)
			}
		}
	}
	collect(parser.Push("answer\n<proposed_"))
	collect(parser.Push("plan>\nstep one\n</proposed_plan>"))
	collect(parser.Finish())
	if visibleBuf.String() != "answer\n" || planBuf.String() != "step one\n" {
		t.Fatalf("stream parser state = visible %q plan %q", visibleBuf.String(), planBuf.String())
	}
	visible, plan := NormalizeProposedPlan("answer\n<proposed_plan>\nstep one\n</proposed_plan>\n")
	if visible != "answer\n" || plan != "step one\n" {
		t.Fatalf("normalized proposed plan = visible %q plan %q", visible, plan)
	}
	visible, plan = NormalizeProposedPlan("<proposed_plan>\na\n</proposed_plan>\n<proposed_plan>\nb\n</proposed_plan>")
	if visible != "" || plan != "b\n" {
		t.Fatalf("last proposed plan should win: visible %q plan %q", visible, plan)
	}
}

func TestProposedPlanParserKeepsInlineTagsVisible(t *testing.T) {
	visible, plan := NormalizeProposedPlan("inline <proposed_plan> tag")
	if visible != "inline <proposed_plan> tag" || plan != "" {
		t.Fatalf("inline tags must remain visible: visible %q plan %q", visible, plan)
	}
}
