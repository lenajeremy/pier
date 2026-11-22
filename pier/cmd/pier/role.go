package main

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// A computer signed in with pier login acts as whoever approved it, with
// their role on the server: the owner, a member, or a viewer, who can look
// but not change anything. The server's own token acts as the owner.

// account is who this computer acts as.
type account struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// whoAmI asks pierd who this computer acts as. A pierd from before roles,
// or the server's own token, reads as the owner.
func whoAmI(ctx context.Context, c config) (account, error) {
	var s struct{ User *account }
	if err := callContext(ctx, c, http.MethodGet, "/api/session", nil, &s); err != nil {
		return account{}, err
	}
	if s.User == nil || s.User.Role == "" {
		return account{Role: "owner"}, nil
	}
	return *s.User, nil
}

// viewerCant is what a viewer is told, in one sentence, when they try
// something only members can do.
func viewerCant(does string) string {
	return "Viewers can't " + does + ". Ask the owner to make you a member."
}

// mayChange says, before any work starts, when this computer's account is
// a viewer's, so pier deploy doesn't pack and upload a folder only to be
// refused. When pierd can't be asked, it lets pierd decide.
func mayChange(c config, does string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if a, err := whoAmI(ctx, c); err == nil && a.Role == "viewer" {
		return errors.New(viewerCant(does))
	}
	return nil
}
