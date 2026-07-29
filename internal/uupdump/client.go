package uupdump

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.uupdump.net"

type Client struct {
	baseURL    string
	httpClient *http.Client
	lastReq    time.Time
}

type Option func(*Client)

func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

func NewClient(opts ...Option) *Client {
	c := &Client{
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) request(ctx context.Context, endpoint string, params url.Values, out any) error {
	since := time.Since(c.lastReq)
	if since < 100*time.Microsecond {
		time.Sleep(100*time.Microsecond - since)
	}

	u := c.baseURL + "/" + endpoint
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}

	c.lastReq = time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var env responseEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decoding envelope: %w", err)
	}

	if err := env.checkError(); err != nil {
		return err
	}

	return json.Unmarshal(env.Response, out)
}

func (c *Client) ListBuilds(ctx context.Context, search string) ([]Build, error) {
	params := url.Values{
		"search":      {search},
		"sortByDate":  {"1"},
	}

	var resp BuildsResponse
	if err := c.request(ctx, "listid.php", params, &resp); err != nil {
		return nil, err
	}
	return resp.Builds, nil
}

func (c *Client) ListLanguages(ctx context.Context, uuid string) (*DetailsResponse, error) {
	params := url.Values{"id": {uuid}}
	var resp DetailsResponse
	if err := c.request(ctx, "listlangs.php", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ListEditions(ctx context.Context, uuid, lang string) (*EditionsResponse, error) {
	params := url.Values{"id": {uuid}, "lang": {lang}}
	var resp EditionsResponse
	if err := c.request(ctx, "listeditions.php", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetPackage(ctx context.Context, uuid, lang string, editions []string) (*PackageResponse, error) {
	params := url.Values{"id": {uuid}, "lang": {lang}}
	for _, ed := range editions {
		params.Add("edition[]", ed)
	}
	var resp PackageResponse
	if err := c.request(ctx, "get.php", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) FindLatestARM64(ctx context.Context) (*Build, error) {
	builds, err := c.ListBuilds(ctx, "windows 11 arm64")
	if err != nil {
		return nil, fmt.Errorf("listing builds: %w", err)
	}

	var arm64 []Build
	for _, b := range builds {
		if b.Arch == "arm64" {
			arm64 = append(arm64, b)
		}
	}
	if len(arm64) == 0 {
		return nil, fmt.Errorf("no ARM64 builds found")
	}

	sort.Slice(arm64, func(i, j int) bool {
		return arm64[i].Created > arm64[j].Created
	})

	// Cumulative/quality updates are delta ESDs — they reference blobs from
	// a base build we don't download. Feature updates have self-contained ESDs.
	for i := range arm64 {
		if !isDeltaBuild(arm64[i].Title) {
			return &arm64[i], nil
		}
	}

	return &arm64[0], nil
}

func isDeltaBuild(title string) bool {
	return strings.Contains(strings.ToLower(title), "update")
}
