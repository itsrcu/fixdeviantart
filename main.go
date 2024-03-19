package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"text/template"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

type deviantArtAPI struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Image     string `json:"url"`
	Author    string `json:"author_name"`
	Thumbnail string `json:"thumbnail_url"`
	VideoHTML string `json:"html"`
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
	Width  any `json:"width"`
	Height any `json:"height"`
}

var (
	embedLinkRegex   = regexp.MustCompile(`(?m)https:\/\/backend\.deviantart\.com\/embed\/film[^"]*`)
	sourcesRegex     = regexp.MustCompile(`(?m)gmon-sources="[^"]*`)
	idealResolutions = []string{"1080p", "720p", "360p"}
)

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

{{if .isVideo}}
<meta property="og:image" content="{{.thumbnail}}"/>
<meta property="og:video" content="{{.image}}"/>
<meta property="og:video:secure_url" content="{{.image}}"/>
<meta property="og:video:width" content="{{.imageWidth}}"/>
<meta property="og:video:height" content="{{.imageHeight}}"/>
<meta property="og:video:type" content="video/mp4"/>
{{else}}
<meta property="og:image" content="{{.image}}"/>
<meta property="og:image:width" content="{{.imageWidth}}"/>
<meta property="og:image:height" content="{{.imageHeight}}"/>
{{end}}

<meta property="og:title" content="{{.title}}"/>
<meta property="og:description" content="{{.title}}"/>
<meta property="og:site_name" content="dxviantart.com"/>

<meta property="twitter:title" content="{{.title}}"/>

{{if .isVideo}}
<meta property="twitter:card" content="player"/>
<meta property="twitter:image" content="0"/>
<meta property="twitter:player:width" content="{{.imageWidth}}"/>
<meta property="twitter:player:height" content="{{.imageHeight}}"/>
<meta property="twitter:player:stream" content="{{.image}}"/>
<meta property="twitter:player:stream:content_type" content="video/mp4"/>
{{else}}
<meta property="twitter:card" content="summary_large_image"/>
<meta property="twitter:image" content="{{.image}}"/>
<meta property="twitter:image:width" content="{{.imageWidth}}"/>
<meta property="twitter:image:height" content="{{.imageHeight}}"/>
{{end}}

{{if not .isTelegramUA}}
<link rel="alternate" href="https://dxviantart.com/ohembed?displayText={{.oembedText}}&author={{.author}}" type="application/json+oembed" title="{{.author}}">
{{end}}
</head>
<body>
{{if or (.isTelegramUA) (.noRedirect)}}
{{if .isVideo}}
<video width="{{.imageWidth}}" height="{{.imageHeight}}" poster="{{.thumbnail}}" controls>
<source src="{{.image}}" type="video/mp4">
Your browser does not support videos :-( (or something broke)
</video>
{{else}}
<img src="{{.image}}">
{{end}}
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

func tryReplaceImage(ctx context.Context, api *deviantArtAPI) bool {
	url := embedLinkRegex.FindString(api.VideoHTML)
	if url == "" {
		return false
	}

	followRequest, followRequestErr := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if followRequestErr != nil {
		log.Println(followRequestErr)
		return false
	}

	followResponse, followResponseErr := http.DefaultClient.Do(followRequest)
	if followResponseErr != nil {
		log.Println(followResponseErr)
		return false
	}
	defer followResponse.Body.Close()

	bytesBody, readErr := io.ReadAll(followResponse.Body)
	if readErr != nil {
		log.Println(readErr)
		return false
	}

	sources := sourcesRegex.FindString(string(bytesBody))
	if sources == "" {
		return false
	}

	sources = strings.ReplaceAll(sources, "gmon-sources=\"", "")
	sources = strings.ReplaceAll(sources, "&quot;", "\"")
	sources = strings.ReplaceAll(sources, "\\/", "/")

	m := make(map[string]struct {
		Src    string `json:"src"`
		Width  int64  `json:"width"`
		Height int64  `json:"height"`
	})

	if unmarshalErr := json.Unmarshal([]byte(sources), &m); unmarshalErr != nil {
		log.Println(unmarshalErr)
		return false
	}

	for i := 0; i < len(idealResolutions); i++ {
		if d, ok := m[idealResolutions[i]]; ok {
			api.Width = d.Width
			api.Height = d.Height
			api.Image = d.Src

			return true
		}
	}

	return false
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
	isVideo := api.Type == "video"

	if isVideo {
		if didWork := tryReplaceImage(timeoutCtx, &api); !didWork {
			// fallback, and hopefully it won't explode
			isVideo = false
		}
	}

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
		"isVideo":   isVideo,
		"thumbnail": api.Thumbnail,
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
