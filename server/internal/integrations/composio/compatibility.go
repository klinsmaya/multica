package composio

import (
	"context"
	"errors"
	"net/url"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	sdk "github.com/multica-ai/multica/server/pkg/composio"
)

type compatibilityClient interface {
	VerifyCompatibilityIdentity(context.Context, string, string) (*sdk.IdentityCompletion, error)
	CompatibilitySessionHeaders(*sdk.CreateSessionResponse, string, bool) (map[string]string, error)
}

func (s *Service) sessionHeaders(resp *sdk.CreateSessionResponse) (map[string]string, error) {
	if s.compat {
		return s.compatClient.CompatibilitySessionHeaders(resp, s.mcpOrigin, s.allowLoopback)
	}
	return s.sdk.MCPAuthHeaders(), nil
}
func (s *Service) CompatibilityMode() bool { return s.compat }

// VerifyBrowserIdentity is called only by a route behind real login middleware.
// A ticket or signed OAuth state alone is never a browser identity assertion.
func (s *Service) VerifyBrowserIdentity(ctx context.Context, user pgtype.UUID, ticket string) (string, error) {
	if !s.compat || !user.Valid || ticket == "" {
		return "", ErrAccountVerification
	}
	subject := util.UUIDToString(user)
	result, err := s.compatClient.VerifyCompatibilityIdentity(ctx, ticket, subject)
	if err != nil {
		return "", ErrAccountVerification
	}
	target, err := url.Parse(result.CallbackURL)
	expected, e := url.Parse(s.callbackURL)
	if err != nil || e != nil || target.User != nil || target.Fragment != "" || target.Scheme != expected.Scheme || target.Host != expected.Host || target.Path != expected.Path {
		return "", ErrAccountVerification
	}
	q, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return "", ErrAccountVerification
	}
	for key, values := range q {
		if len(values) != 1 || (key != "state" && key != "status" && key != "connected_account_id") {
			return "", ErrAccountVerification
		}
	}
	claims, err := verifyState(s.secret, q.Get("state"), s.now())
	if err != nil || claims.UserID != subject || q.Get("status") != "success" || q.Get("connected_account_id") != result.ConnectedAccountID {
		return "", ErrAccountVerification
	}
	slug, err := s.CompleteCallback(ctx, q.Get("state"), "success", result.ConnectedAccountID)
	if err != nil {
		return "", errors.New("composio: verified connection could not be saved")
	}
	return s.CallbackRedirect(slug, true), nil
}
