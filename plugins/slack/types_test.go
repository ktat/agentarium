package slack

import "testing"

func TestAccessTokenMasksString(t *testing.T) {
	at, err := NewAccessToken("xoxp-secret")
	if err != nil {
		t.Fatalf("NewAccessToken: %v", err)
	}
	if at.String() != "***" {
		t.Errorf("String() = %q, want ***", at.String())
	}
	if at.Reveal() != "xoxp-secret" {
		t.Errorf("Reveal() = %q, want xoxp-secret", at.Reveal())
	}
	if _, err := NewAccessToken(""); err == nil {
		t.Error("empty token should error")
	}
}

func TestNewOAuthStateUnique(t *testing.T) {
	s1, err := NewOAuthState()
	if err != nil {
		t.Fatalf("NewOAuthState: %v", err)
	}
	s2, _ := NewOAuthState()
	if s1.String() == "" || s1 == s2 {
		t.Errorf("states should be non-empty and unique: %q %q", s1, s2)
	}
}

func TestUserBestName(t *testing.T) {
	cases := []struct {
		u    User
		want string
	}{
		{User{DisplayName: "disp", RealName: "real", Name: "name"}, "disp"},
		{User{RealName: "real", Name: "name"}, "real"},
		{User{Name: "name"}, "name"},
	}
	for _, c := range cases {
		if got := c.u.BestName(); got != c.want {
			t.Errorf("BestName() = %q, want %q", got, c.want)
		}
	}
}
