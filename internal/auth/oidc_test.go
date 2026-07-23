package auth

import (
	"reflect"
	"testing"
)

func TestIdentityFromClaims(t *testing.T) {
	cases := []struct {
		name        string
		claims      map[string]any
		groupsClaim string
		subject     string
		wantUser    string
		wantEmail   string
		wantGroups  []string
	}{
		{
			name: "preferred_username + array groups",
			claims: map[string]any{
				"preferred_username": "alice",
				"email":              "alice@corp.example",
				"groups":             []any{"vphone-admins", "staff"},
			},
			groupsClaim: "groups",
			wantUser:    "alice", wantEmail: "alice@corp.example",
			wantGroups: []string{"vphone-admins", "staff"},
		},
		{
			name:        "falls back to email then subject",
			claims:      map[string]any{"email": "bob@corp.example"},
			groupsClaim: "groups",
			subject:     "sub-123",
			wantUser:    "bob@corp.example", wantEmail: "bob@corp.example",
			wantGroups: nil,
		},
		{
			name:        "subject-only",
			claims:      map[string]any{},
			groupsClaim: "groups", subject: "sub-999",
			wantUser: "sub-999",
		},
		{
			name: "custom groups claim, space-delimited string",
			claims: map[string]any{
				"preferred_username": "carol",
				"roles":              "vphone-user editor",
			},
			groupsClaim: "roles",
			wantUser:    "carol",
			wantGroups:  []string{"vphone-user", "editor"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := identityFromClaims(tc.claims, tc.groupsClaim, tc.subject)
			if id.Username != tc.wantUser {
				t.Errorf("username = %q, want %q", id.Username, tc.wantUser)
			}
			if id.Email != tc.wantEmail {
				t.Errorf("email = %q, want %q", id.Email, tc.wantEmail)
			}
			if len(id.Groups) != len(tc.wantGroups) || (len(id.Groups) > 0 && !reflect.DeepEqual(id.Groups, tc.wantGroups)) {
				t.Errorf("groups = %v, want %v", id.Groups, tc.wantGroups)
			}
		})
	}
}
