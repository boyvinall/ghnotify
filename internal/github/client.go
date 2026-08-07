package github

import (
	"context"
	"errors"
	"net/http"
	"strings"

	ghapi "github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

// Client wraps go-github with our host information.
type Client struct {
	inner          *ghapi.Client
	host           string
	excludeQuery   string   // pre-built "-author:X -author:Y" fragment
	excludeAuthors []string // raw list, for client-side filtering (e.g. notifications)
}

// NewClient returns a token-authenticated client for host.
// Use "github.com" for github.com or a bare hostname for GitHub Enterprise.
// excludeAuthors is a list of author logins (e.g. "app/renovate") to exclude from queries.
func NewClient(host, token string, excludeAuthors []string) *Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)

	var inner *ghapi.Client
	if host == "github.com" {
		inner = ghapi.NewClient(tc)
	} else {
		inner, _ = ghapi.NewClient(tc).WithEnterpriseURLs(
			"https://"+host+"/api/v3/",
			"https://"+host+"/api/uploads/",
		)
	}

	parts := make([]string, 0, len(excludeAuthors))
	for _, a := range excludeAuthors {
		if a != "" {
			parts = append(parts, "-author:"+a)
		}
	}
	return &Client{
		inner:          inner,
		host:           host,
		excludeQuery:   strings.Join(parts, " "),
		excludeAuthors: excludeAuthors,
	}
}

// isExcludedAuthor reports whether login matches one of the configured
// excluded authors. excludeAuthors entries use GitHub search syntax (e.g.
// "app/renovate"), but the REST API reports bot logins as "renovate[bot]",
// so an "app/" prefix is also matched against the "<name>[bot]" form.
func (c *Client) isExcludedAuthor(login string) bool {
	for _, a := range c.excludeAuthors {
		if a == login {
			return true
		}
		if name, ok := strings.CutPrefix(a, "app/"); ok && login == name+"[bot]" {
			return true
		}
	}
	return false
}

// IsUnauthorized reports whether err is a 401 from the GitHub API.
func IsUnauthorized(err error) bool {
	var ghErr *ghapi.ErrorResponse
	return errors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusUnauthorized
}
