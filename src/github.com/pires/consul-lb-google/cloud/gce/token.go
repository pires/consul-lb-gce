package gce

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
)

type altTokenSource struct {
	oauthClient *http.Client
	tokenURL    string
	tokenBody   string
}

func (a *altTokenSource) Token() (*oauth2.Token, error) {
	req, err := http.NewRequest("POST", a.tokenURL, strings.NewReader(a.tokenBody))
	if err != nil {
		return nil, err
	}
	res, err := a.oauthClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if err := googleapi.CheckResponse(res); err != nil {
		return nil, err
	}
	var tok struct {
		AccessToken string    `json:"accessToken"`
		ExpireTime  time.Time `json:"expireTime"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tok); err != nil {
		return nil, err
	}
	return &oauth2.Token{
		AccessToken: tok.AccessToken,
		Expiry:      tok.ExpireTime,
	}, nil
}

func newAltTokenSource(tokenURL, tokenBody string) oauth2.TokenSource {
	client := oauth2.NewClient(oauth2.NoContext, google.ComputeTokenSource(""))
	a := &altTokenSource{
		oauthClient: client,
		tokenURL:    tokenURL,
		tokenBody:   tokenBody,
	}
	return oauth2.ReuseTokenSource(nil, a)
}
