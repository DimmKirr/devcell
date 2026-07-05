package serve

import (
	"fmt"
	"net/http"
)

const discoveryTemplate = `<?xml version="1.0" encoding="utf-8"?>
<wkspFeeds>
  <wkspFeed>
    <url>%s</url>
  </wkspFeed>
</wkspFeeds>`

func newFeedDiscoveryHandler(publicHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}

		host := resolveHostWithPort(publicHost, r)
		feedURL := fmt.Sprintf("%s://%s/RDWeb/Feed/webfeed.aspx", scheme, host)

		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		fmt.Fprintf(w, discoveryTemplate, feedURL)
	})
}

func resolveHostWithPort(configured string, r *http.Request) string {
	if configured != "" {
		return configured
	}
	return r.Host
}
