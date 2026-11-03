package accounts

import (
	"encoding/json"
	"strings"
	"testing"

	"dawngate/server/internal/store"
)

// the account's data download carries its claims on the issue tracker; the account's deletion removes them.
func TestExportAndDeleteClaims(t *testing.T) {
	st := store.New()
	defer st.Close()
	a, pw, err := CreateAccount(st, "voter", t0)
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := CreateAccount(st, "other", t0)
	if _, err := st.SyncIssues([]store.IssueText{{ID: "UI1", Title: "The menu's music", Order: 1}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutClaim("UI1", a.UserID, store.StanceWrong, "it plays later",
		[]store.ClaimVideo{{VideoID: "dQw4w9WgXcQ", Seconds: 83}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutClaim("UI1", other.UserID, store.StanceCorrect, "someone else's", nil, t0); err != nil {
		t.Fatal(err)
	}
	exp, r := ExportAccount(st, "voter", pw, t0)
	if r.Code != 0 || len(exp.Claims) != 1 || exp.Claims[0].IssueID != "UI1" || exp.Claims[0].Note != "it plays later" {
		t.Fatalf("export %+v %+v", exp.Claims, r)
	}
	b, err := json.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"issueClaims":[{`, `"issue":"UI1"`, `"issueTitle":"The menu's music"`, `"stance":"wrong"`,
		`"video":"dQw4w9WgXcQ"`, `"seconds":83`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the export lacks %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "someone else's") {
		t.Error("another account's claim exported")
	}

	if r := DeleteAccount(st, "voter", pw); r.Code != 0 {
		t.Fatalf("delete %+v", r)
	}
	if cs, err := st.ClaimsOf(a.UserID); err != nil || len(cs) != 0 {
		t.Errorf("claims after the deletion %+v %v", cs, err)
	}
	if cs, _ := st.ClaimsOf(other.UserID); len(cs) != 1 {
		t.Errorf("the other's claims %+v", cs)
	}
}
