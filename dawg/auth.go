package dawg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type auth struct {
	username string
	password string
	token    *token
	cli      *client
}

const (
	oauthEndpoint = "https://api.dominos.com/as/token.oauth2"
	loginEndpoint = "https://order.dominos.com/power/login"
)

var (
	// As of May 9, 2020, it was discovered that the authentication endpoint was changed from,
	// "api.dominos.com/as/token.oauth2"
	// to,
	// "authproxy.dominos.com/auth-proxy-service/login".
	// I am documenting this change just in case it every changed back in the future.
	//
	// TODO: See comment above. Possible solutions are try both oauth endpoints or let users specify which to use.
	oauthURL = &url.URL{
		Scheme: "https",
		Host:   "authproxy.dominos.com",
		Path:   "/auth-proxy-service/login",
	}

	loginURL = &url.URL{
		Scheme: "https",
		Host:   orderHost,
		Path:   "/power/login",
	}
)

func newauth(username, password string) (*auth, error) {
	tok, err := gettoken(username, password)
	if err != nil {
		return nil, err
	}
	a := &auth{
		token:    tok,
		username: username,
		password: password,
		cli: &client{
			host: orderHost,
			Client: &http.Client{
				Transport:     tok,
				Timeout:       60 * time.Second,
				CheckRedirect: noRedirects,
			},
		},
	}
	return a, nil
}

var noRedirects = func(r *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

const tokenHost = "api.dominos.com"

type token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Type         string `json:"token_type"`

	// ExpiresIn is the time in seconds that it takes for the token to
	// expire.
	ExpiresIn int `json:"expires_in"`

	transport http.RoundTripper
}

func (t *token) authorization() string {
	return fmt.Sprintf("%s %s", t.Type, t.AccessToken)
}

func (t *token) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("Authorization", t.authorization())
	return t.transport.RoundTrip(req)
}

var scopes = []string{
	"customer:card:read",
	"customer:profile:read:extended",
	"customer:orderHistory:read",
	"customer:card:update",
	"customer:profile:read:basic",
	"customer:loyalty:read",
	"customer:orderHistory:update",
	"customer:card:create",
	"customer:loyaltyHistory:read",
	"order:place:cardOnFile",
	"customer:card:delete",
	"customer:orderHistory:create",
	"customer:profile:update",
	"easyOrder:optInOut",
	"easyOrder:read",
}

func gettoken(username, password string) (*token, error) {
	data := url.Values{
		"grant_type":   {"password"},
		"client_id":    {"nolo-rm"}, // nolo-rm if you want a refresh token, or just nolo for temporary token
		"validator_id": {"VoldemortCredValidator"},
		"scope":        {strings.Join(scopes, " ")},
		"username":     {username},
		"password":     {password},
	}
	req := newAuthRequest(oauthURL, data)
	resp, err := orderClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"dawg.gettoken: bad status code %d", resp.StatusCode)
	}
	tok := &token{transport: http.DefaultTransport}
	return tok, unmarshalToken(resp.Body, tok)
}

func (a *auth) login() (*UserProfile, error) {
	data := url.Values{
		"loyaltyIsActive": {"true"},
		"rememberMe":      {"true"},
		"u":               {a.username},
		"p":               {a.password},
	}
	req := newAuthRequest(loginURL, data)
	res, err := a.cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	profile := &UserProfile{auth: a}
	b, err := ioutil.ReadAll(res.Body)
	if err = errpair(err, dominosErr(b)); err != nil {
		return nil, err
	}
	return profile, json.Unmarshal(b, profile)
}

func newAuthRequest(u *url.URL, vals url.Values) *http.Request {
	return &http.Request{
		Method:     "POST",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Host:       u.Host,
		Header: http.Header{
			"Content-Type": {
				"application/x-www-form-urlencoded; charset=UTF-8"},
			"User-Agent": {
				"Apizza Dominos API Wrapper for Go " + time.Now().UTC().String()},
		},
		URL:  u,
		Body: ioutil.NopCloser(strings.NewReader(vals.Encode())),
	}
}

type client struct {
	*http.Client
	host string
}

func (c *client) do(req *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dawg.client.do: bad status code %d", resp.StatusCode)
	}
	_, err = buf.ReadFrom(resp.Body)
	if bytes.HasPrefix(bytes.ToLower(buf.Bytes()[:15]), []byte("<!doctype html>")) {
		return nil, errpair(err, errors.New("got html response"))
	}
	return buf.Bytes(), err
}

func (c *client) dojson(v interface{}, r *http.Request) (err error) {
	resp, err := c.Do(r)
	if err != nil {
		return err
	}
	defer func() {
		e := resp.Body.Close()
		if err == nil {
			err = e
		}
	}()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (c *client) get(path string, params URLParam) ([]byte, error) {
	if params == nil {
		params = &Params{}
	}
	return c.do(&http.Request{
		Method: "GET",
		Host:   c.host,
		Proto:  "HTTP/1.1",
		Header: make(http.Header),
		URL: &url.URL{
			Scheme:   "https",
			Host:     c.host,
			Path:     path,
			RawQuery: params.Encode(),
		},
	})
}

func (c *client) post(path string, params URLParam, r io.Reader) ([]byte, error) {
	if params == nil {
		params = &Params{}
	}
	rc, ok := r.(io.ReadCloser)
	if !ok && r != nil {
		rc = ioutil.NopCloser(r)
	}
	return c.do(&http.Request{
		Method: "POST",
		Host:   c.host,
		Proto:  "HTTP/1.1",
		Header: make(http.Header),
		Body:   rc,
		URL: &url.URL{
			Scheme:   "https",
			Host:     c.host,
			Path:     path,
			RawQuery: params.Encode(),
		},
	})
}

func unmarshalToken(r io.ReadCloser, t *token) error {
	buf := new(bytes.Buffer)
	defer r.Close()

	_, e1 := buf.ReadFrom(r)
	err := errpair(e1, json.Unmarshal(buf.Bytes(), t))
	if err != nil {
		return err
	}
	return newTokenErr(buf.Bytes())
}

type tokenError struct {
	Err       string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

func (e *tokenError) Error() string {
	return fmt.Sprintf("%s: %s", e.Err, e.ErrorDesc)
}

func newTokenErr(b []byte) error {
	e := &tokenError{}
	// if there is no error the the json parsing will fail
	json.Unmarshal(b, e)
	if len(e.Err) > 0 {
		return e
	}
	return nil
}
