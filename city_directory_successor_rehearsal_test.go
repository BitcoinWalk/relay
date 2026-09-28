package main

import "testing"

func TestCityDirectorySuccessorRehearsal(t *testing.T) {
	report, err := rehearseCityDirectorySuccessors()
	if err != nil {
		t.Fatal(err)
	}
	if report.CityID == "" || report.RootEventID == "" || len(report.Transitions) != 4 {
		t.Fatalf("incomplete rehearsal report: %#v", report)
	}
	want := []string{"owner-update", "operator-update", "rotate", "recover"}
	for index, transition := range report.Transitions {
		if transition.Name != want[index] || transition.Sequence != uint64(index+1) || transition.EventID == "" || transition.SignerPubkey == "" {
			t.Fatalf("unexpected transition %d: %#v", index, transition)
		}
	}
	if !report.OperatorEscalationRejected || !report.RotatedOwnerRejected || !report.ConflictingSuccessorsRejected || report.FinalOwnerPubkey == report.InitialOwnerPubkey || report.FinalOperatorCount != 0 || report.FinalSequence != 4 || report.ChainLength != 5 {
		t.Fatalf("unsafe rehearsal result: %#v", report)
	}
}
