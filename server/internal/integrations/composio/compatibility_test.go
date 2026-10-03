package composio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	sdk "github.com/multica-ai/multica/server/pkg/composio"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCompatibilityIdentityChecksLoggedInUserAndReusesOwnershipValidation(t *testing.T) {
	for _, badUser := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "wrong login"}[badUser], func(t *testing.T) {
			user := mintUUID(1)
			userID := util.UUIDToString(user)
			now := time.Now()
			state, err := signState(testSecret, stateClaims{UserID: userID, ToolkitSlug: "notion", AuthConfigID: "ac_notion", Exp: now.Add(time.Minute).Unix()})
			if err != nil {
				t.Fatal(err)
			}
			seenSubject := ""
			accountStatus := "ACTIVE"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v3.1/internal/connect/complete":
					var input map[string]string
					_ = json.NewDecoder(r.Body).Decode(&input)
					seenSubject = input["subject"]
					_ = json.NewEncoder(w).Encode(sdk.IdentityCompletion{ConnectedAccountID: "ca", CallbackURL: "https://multica.ai/api/integrations/composio/callback?state=" + url.QueryEscape(state) + "&status=success&connected_account_id=ca"})
				case "/api/v3.1/connected_accounts":
					_ = json.NewEncoder(w).Encode(sdk.ListConnectedAccountsResponse{Items: []sdk.ConnectedAccount{{ID: "ca", UserID: userID, AuthConfigID: "ac_notion", AuthConfig: sdk.AuthConfigRef{ID: "ac_notion"}, Toolkit: sdk.Toolkit{Slug: "notion"}, Status: accountStatus}}})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client, _ := sdk.NewClient(sdk.Options{APIKey: "control", BaseURL: server.URL + "/api/v3.1", Backend: "compat", AllowInsecureLoopback: true})
			store := &flakyCompatibilityStore{fakeStore: newFakeStore(), fail: !badUser}
			service, err := NewService(client, store, Config{Backend: "compat", MCPOrigin: "https://mcp.example", StateSecret: testSecret, CallbackBaseURL: "https://multica.ai", FrontendBaseURL: "https://multica.ai"})
			if err != nil {
				t.Fatal(err)
			}
			login := user
			if badUser {
				login = mintUUID(2)
			}
			if !badUser {
				if _, e := service.VerifyBrowserIdentity(context.Background(), login, "ticket"); e == nil {
					t.Fatal("mirror failure was hidden")
				}
			}
			target, err := service.VerifyBrowserIdentity(context.Background(), login, "ticket")
			if seenSubject != util.UUIDToString(login) {
				t.Fatal("browser supplied identity used")
			}
			if badUser {
				if err == nil {
					t.Fatal("wrong login accepted")
				}
			} else if err != nil || target == "" {
				t.Fatalf("verification %s %v", target, err)
			}
			if !badUser {
				accountStatus = "INITIATED"
				if _, e := service.CompleteCallback(context.Background(), state, "success", "ca"); e == nil {
					t.Fatal("pending account accepted by public callback")
				}
			}
		})
	}
}

type flakyCompatibilityStore struct {
	*fakeStore
	fail bool
}

func (s *flakyCompatibilityStore) UpsertUserComposioConnection(ctx context.Context, arg db.UpsertUserComposioConnectionParams) (db.UserComposioConnection, error) {
	if s.fail {
		s.fail = false
		return db.UserComposioConnection{}, errors.New("fixture temporary store failure")
	}
	return s.fakeStore.UpsertUserComposioConnection(ctx, arg)
}
