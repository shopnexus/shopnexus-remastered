package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"

	"github.com/gorilla/pat"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/openidConnect"
)

func main() {
	// because the OpenID Connect provider initialize itself in the New(), it can return an error which should be handled or ignored
	// ignore the error for now
	openidConnect, _ := openidConnect.New(os.Getenv("OPENID_CONNECT_KEY"), os.Getenv("OPENID_CONNECT_SECRET"), "http://localhost:3000/auth/openid-connect/callback", os.Getenv("OPENID_CONNECT_DISCOVERY_URL"))
	if openidConnect != nil {
		goth.UseProviders(openidConnect)
	}

	m := map[string]string{
		"amazon":          "Amazon",
		"apple":           "Apple",
		"auth0":           "Auth0",
		"azuread":         "Azure AD",
		"battlenet":       "Battle.net",
		"bitbucket":       "Bitbucket",
		"box":             "Box",
		"dailymotion":     "Dailymotion",
		"deezer":          "Deezer",
		"digitalocean":    "Digital Ocean",
		"discord":         "Discord",
		"dropbox":         "Dropbox",
		"eveonline":       "Eve Online",
		"facebook":        "Facebook",
		"fitbit":          "Fitbit",
		"gitea":           "Gitea",
		"github":          "Github",
		"gitlab":          "Gitlab",
		"google":          "Google",
		"gplus":           "Google Plus",
		"heroku":          "Heroku",
		"instagram":       "Instagram",
		"intercom":        "Intercom",
		"kakao":           "Kakao",
		"lastfm":          "Last FM",
		"line":            "LINE",
		"linkedin":        "LinkedIn",
		"mastodon":        "Mastodon",
		"meetup":          "Meetup.com",
		"microsoftonline": "Microsoft Online",
		"naver":           "Naver",
		"nextcloud":       "NextCloud",
		"okta":            "Okta",
		"onedrive":        "Onedrive",
		"openid-connect":  "OpenID Connect",
		"patreon":         "Patreon",
		"paypal":          "Paypal",
		"salesforce":      "Salesforce",
		"seatalk":         "SeaTalk",
		"shopify":         "Shopify",
		"slack":           "Slack",
		"soundcloud":      "SoundCloud",
		"spotify":         "Spotify",
		"steam":           "Steam",
		"strava":          "Strava",
		"stripe":          "Stripe",
		"tiktok":          "TikTok",
		"twitch":          "Twitch",
		"twitter":         "Twitter",
		"twitterv2":       "Twitter",
		"typetalk":        "Typetalk",
		"uber":            "Uber",
		"vk":              "VK",
		"wecom":           "WeCom",
		"wepay":           "Wepay",
		"xero":            "Xero",
		"yahoo":           "Yahoo",
		"yammer":          "Yammer",
		"yandex":          "Yandex",
		"zoom":            "Zoom",
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	providerIndex := &ProviderIndex{Providers: keys, ProvidersMap: m}

	p := pat.New()
	p.Get("/auth/{provider}/callback", func(res http.ResponseWriter, req *http.Request) {

		user, err := gothic.CompleteUserAuth(res, req)
		if err != nil {
			fmt.Fprintln(res, err)
			return
		}

		t, _ := template.New("foo").Parse(userTemplate)
		t.Execute(res, user)
	})

	p.Get("/logout/{provider}", func(res http.ResponseWriter, req *http.Request) {
		gothic.Logout(res, req)
		res.Header().Set("Location", "/")
		res.WriteHeader(http.StatusTemporaryRedirect)
	})

	p.Get("/auth/{provider}", func(res http.ResponseWriter, req *http.Request) {
		// try to get the user without re-authenticating
		if gothUser, err := gothic.CompleteUserAuth(res, req); err == nil {
			t, _ := template.New("foo").Parse(userTemplate)
			t.Execute(res, gothUser)
		} else {
			gothic.BeginAuthHandler(res, req)
		}
	})

	p.Get("/", func(res http.ResponseWriter, req *http.Request) {
		t, _ := template.New("foo").Parse(indexTemplate)
		t.Execute(res, providerIndex)
	})

	log.Println("listening on localhost:3000")
	log.Fatal(http.ListenAndServe(":3000", p))
}

type ProviderIndex struct {
	Providers    []string
	ProvidersMap map[string]string
}

var indexTemplate = `{{range $key,$value:=.Providers}}
    <p><a href="/auth/{{$value}}">Log in with {{index $.ProvidersMap $value}}</a></p>
{{end}}`

var userTemplate = `
<p><a href="/logout/{{.Provider}}">logout</a></p>
<p>Name: {{.Name}} [{{.LastName}}, {{.FirstName}}]</p>
<p>Email: {{.Email}}</p>
<p>NickName: {{.NickName}}</p>
<p>Location: {{.Location}}</p>
<p>AvatarURL: {{.AvatarURL}} <img src="{{.AvatarURL}}"></p>
<p>Description: {{.Description}}</p>
<p>UserID: {{.UserID}}</p>
<p>AccessToken: {{.AccessToken}}</p>
<p>ExpiresAt: {{.ExpiresAt}}</p>
<p>RefreshToken: {{.RefreshToken}}</p>
`
