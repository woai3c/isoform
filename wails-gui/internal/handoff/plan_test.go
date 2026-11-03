package handoff

import "testing"

func TestPlanFromBodyReadsTheThreeParts(t *testing.T) {
	body := "# Draft the memo\n\n## Desired result\nA one-page memo for Tuesday\n\n## Included\n- The Q3 numbers\n\n## Isoform will not\n- Send it\n\n## Boundaries\nOnly the shared folder.\n\n## Access needed\n- Read the Q3 folder\n\n## Success criteria\n- It is one page\n"
	plan, ok := PlanFromBody(body)
	if !ok || plan.Will != "A one-page memo for Tuesday. The Q3 numbers." || plan.Wont != "Send it. Only the shared folder." || plan.Need != "Read the Q3 folder." {
		t.Fatalf("plan = %+v, %v", plan, ok)
	}
}

func TestPlanFromBodyLeavesOutWhatIsNotNeeded(t *testing.T) {
	plan, ok := PlanFromBody("## Desired result\nA memo.\n## Access needed\nNone\n")
	if !ok || plan.Need != "" || plan.Wont != "" {
		t.Fatalf("plan = %+v, %v", plan, ok)
	}
	if _, ok := PlanFromBody("Just some notes"); ok {
		t.Fatal("a brief without Desired result has a plan")
	}
}
