package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"text/template"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

type deviantArtAPI struct {
	Title     string `json:"title"`
	Image     string `json:"url"`
	Author    string `json:"author_name"`
	Community struct {
		Statistics struct {
			Attributes struct {
				Views     int64 `json:"views"`
				Favorites int64 `json:"favorites"`
				Comments  int64 `json:"comments"`
				Downloads int64 `json:"downloads"`
			} `json:"_attributes"`
		} `json:"statistics"`
	} `json:"community"`
	Width  int64 `json:"width"`
	Height int64 `json:"height"`
}

const (
	minuteTimeout = 1 * time.Minute

	robotsTxt = `
User-Agent: *
Disallow: /
`
	// Thanks!
	// https://github.com/daisyUniverse/fxdeviantart
	// and
	// https://github.com/FixTweet/FxTwitter
	staticTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
<meta content="text/html; charset=UTF-8" http-equiv="Content-Type"/>
<meta property="theme-color" content="{{.randomHex}}"/>
{{if and (not .isTelegramUA) (not .noRedirect)}}
<meta http-equiv="refresh" content="0;url={{.baseURL}}"/>
{{end}}

<meta property="og:url" content="{{.baseURL}}"/>
<meta property="og:image" content="{{.image}}"/>
<meta property="og:title" content="{{.title}}"/>
<meta property="og:description" content="{{.title}}"/>
<meta property="og:image:width" content="{{.imageWidth}}"/>
<meta property="og:image:height" content="{{.imageHeight}}"/>
<meta property="og:site_name" content="dxviantart.com"/>

<meta property="twitter:card" content="summary_large_image"/>
<meta property="twitter:title" content="{{.title}}"/>
<meta property="twitter:image" content="{{.image}}"/>
<meta property="twitter:image:width" content="{{.imageWidth}}"/>
<meta property="twitter:image:height" content="{{.imageHeight}}"/>

{{if not .isTelegramUA}}
<link rel="alternate" href="https://dxviantart.com/ohembed?displayText={{.oembedText}}&author={{.author}}" type="application/json+oembed" title="{{.author}}">
{{end}}
</head>
<body>
{{if or (.isTelegramUA) (.noRedirect)}}
<img src="{{.image}}">
{{else}}
<p>Redirecting, this should only take a second...</p><br><p>Not redirecting? <a href="{{.baseURL}}">Click here.</a></p>
{{end}}
</body>
</html>`
)

func rngHex() string {
	hexCode := make([]byte, 3)
	if _, readErr := rand.Read(hexCode); readErr != nil {
		return "#015196"
	}

	return "#" + hex.EncodeToString(hexCode)
}

// https://stackoverflow.com/questions/10599933/convert-long-number-into-abbreviated-string-in-javascript-with-a-special-shortn
func formatNumber(number float64) string {
	if number < 1e3 {
		return fmt.Sprintf("%.0f", number)
	}

	if number >= 1e3 && number < 1e6 {
		return fmt.Sprintf("%.1fK", number/1e3)
	}

	if number >= 1e6 && number < 1e9 {
		return fmt.Sprintf("%.1fM", number/1e6)
	}

	if number >= 1e9 && number < 1e12 {
		return fmt.Sprintf("%.1fB", number/1e9)
	}

	return fmt.Sprintf("%.1fT", number/1e12)
}

func getImage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Redirect(w, r, "https://github.com/itsrcu/fixdeviantart", http.StatusFound)
		return
	}

	timeoutCtx, cancel := context.WithTimeout(r.Context(), minuteTimeout)
	defer cancel()

	baseURL := "https://deviantart.com" + r.URL.Path

	apiRequest, apiRequestErr := http.NewRequestWithContext(timeoutCtx, http.MethodGet, "https://backend.deviantart.com/oembed?url="+baseURL, http.NoBody)
	if apiRequestErr != nil {
		log.Println(apiRequestErr)

		http.Error(w, "failed to create request to deviantart", http.StatusInternalServerError)
		return
	}

	apiResponse, apiResponseErr := http.DefaultClient.Do(apiRequest)
	if apiResponseErr != nil {
		log.Println(apiResponseErr)

		http.Error(w, "failed to do request to deviantart", http.StatusInternalServerError)
		return
	}
	defer apiResponse.Body.Close()

	var api deviantArtAPI

	if decodeErr := json.NewDecoder(apiResponse.Body).Decode(&api); decodeErr != nil {
		log.Println(decodeErr)

		http.Error(w, "failed to decode api response", http.StatusInternalServerError)
		return
	}

	isTelegramUA := strings.Contains(r.UserAgent(), "Telegram")
	noRedirect := r.URL.Query().Get("staypls") == "1"

	daTemplate, parseErr := template.New("dxviantart").Parse(staticTemplate)
	if parseErr != nil {
		log.Println(parseErr)

		http.Error(w, "failed to parse template", http.StatusInternalServerError)
		return
	}

	if execErr := daTemplate.Execute(w, map[string]any{
		"isTelegramUA": isTelegramUA,
		"noRedirect":   noRedirect,
		"baseURL":      baseURL,
		"image":        api.Image,
		"title":        api.Title + " by " + api.Author,
		"imageWidth":   api.Width,
		"imageHeight":  api.Height,
		"oembedText": fmt.Sprintf("👁️  %s  ❤️ %s  💬 %s  ⬇️ %s",
			formatNumber(float64(api.Community.Statistics.Attributes.Views)),
			formatNumber(float64(api.Community.Statistics.Attributes.Favorites)),
			formatNumber(float64(api.Community.Statistics.Attributes.Comments)),
			formatNumber(float64(api.Community.Statistics.Attributes.Downloads)),
		),
		"author":    api.Author,
		"randomHex": rngHex(),
	}); execErr != nil {
		log.Println(execErr)

		http.Error(w, "failed to parse template", http.StatusInternalServerError)
		return
	}
}

// Thanks! https://github.com/FixTweet/FxTwitter
func genoEmbed(w http.ResponseWriter, r *http.Request) {
	fallbackAuthor := "https://deviantart.com/"
	if r.URL.Query().Has("author") {
		fallbackAuthor += r.URL.Query().Get("author")
	}

	displayText := "DeviantArt"
	if r.URL.Query().Has("displayText") {
		displayText = r.URL.Query().Get("displayText")
	}

	result, resultErr := json.Marshal(
		struct {
			AuthorName   string `json:"author_name"`
			AuthorURL    string `json:"author_url"`
			ProviderName string `json:"provider_name"`
			ProviderURL  string `json:"provider_url"`
			Title        string `json:"title"`
			Type         string `json:"type"`
			Version      string `json:"version"`
		}{
			AuthorName:   displayText,
			AuthorURL:    fallbackAuthor,
			ProviderName: "DxviantArt",
			ProviderURL:  "https://github.com/itsrcu/fixdeviantart",
			Title:        "DeviantArt",
			Type:         "link",
			Version:      "1.0",
		},
	)
	if resultErr != nil {
		http.Error(w, "unable to process the request", http.StatusBadRequest)
		return
	}

	fmt.Fprint(w, string(result))
}

func robots(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, robotsTxt)
}

func main() {
	muxHandler := http.NewServeMux()
	muxHandler.HandleFunc("/", getImage)
	muxHandler.HandleFunc("/ohembed", genoEmbed)
	muxHandler.HandleFunc("/robots.txt", robots)
	muxHandler.HandleFunc("/favicon.ico", http.NotFound)

	certManager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist("www.dxviantart.com", "dxviantart.com"),
		Cache:      autocert.DirCache("./certs"),
	}

	go func() {
		redirectServer := &http.Server{
			Addr:              ":80",
			Handler:           certManager.HTTPHandler(nil),
			ReadTimeout:       minuteTimeout,
			ReadHeaderTimeout: minuteTimeout,
			WriteTimeout:      minuteTimeout,
			IdleTimeout:       minuteTimeout,
		}

		log.Println("http server started on port :80")

		if httpErr := redirectServer.ListenAndServe(); httpErr != nil {
			panic(httpErr)
		}
	}()

	mainServer := &http.Server{
		Addr:              ":443",
		Handler:           muxHandler,
		TLSConfig:         certManager.TLSConfig(),
		ReadTimeout:       minuteTimeout,
		ReadHeaderTimeout: minuteTimeout,
		WriteTimeout:      minuteTimeout,
		IdleTimeout:       minuteTimeout,
	}

	log.Println("https server started on port :443")

	if listenErr := mainServer.ListenAndServeTLS("", ""); listenErr != nil {
		panic(listenErr)
	}
}
