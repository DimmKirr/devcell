package mctcatalog

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	Win11CatalogURL = "https://go.microsoft.com/fwlink?linkid=2156292"
	WorProjectURL   = "https://worproject.com/dldserv/esd/getversions.php"
)

type ESDFile struct {
	FileName     string
	LanguageCode string
	Edition      string
	Architecture string
	Size         int64
	SHA1         string
	FilePath     string // Microsoft CDN download URL
}

type Client struct {
	httpClient *http.Client
	logFunc    func(format string, args ...any)
}

type Option func(*Client)

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

func WithLogFunc(f func(string, ...any)) Option {
	return func(c *Client) { c.logFunc = f }
}

func NewClient(opts ...Option) *Client {
	c := &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) logf(format string, args ...any) {
	if c.logFunc != nil {
		c.logFunc(format, args...)
	}
}

// FindARM64ESD fetches the MCT catalog and returns the ESD entry matching
// the given language and edition for ARM64.
func (c *Client) FindARM64ESD(ctx context.Context, language, edition string) (*ESDFile, error) {
	catalogURL := Win11CatalogURL

	// Try worproject.com for the latest catalog URL first.
	if latestURL, err := c.latestCatalogURL(ctx); err == nil && latestURL != "" {
		c.logf("using worproject.com catalog URL: %s", latestURL)
		catalogURL = latestURL
	} else {
		c.logf("worproject.com unavailable, using default: %s", catalogURL)
	}

	productsXML, err := c.fetchAndExtractCatalog(ctx, catalogURL)
	if err != nil {
		return nil, fmt.Errorf("fetching MCT catalog: %w", err)
	}

	files, err := parseProductsXML(productsXML)
	if err != nil {
		return nil, fmt.Errorf("parsing products.xml: %w", err)
	}

	c.logf("parsed %d ESD entries from MCT catalog", len(files))

	langLower := strings.ToLower(language)
	edLower := strings.ToLower(edition)

	for _, f := range files {
		if strings.EqualFold(f.Architecture, "ARM64") &&
			strings.EqualFold(f.LanguageCode, langLower) &&
			strings.EqualFold(f.Edition, edLower) {
			c.logf("found MCT ESD: %s (%d bytes, arch=%s, lang=%s, edition=%s)",
				f.FileName, f.Size, f.Architecture, f.LanguageCode, f.Edition)
			return &f, nil
		}
	}

	// Relaxed match: just ARM64 + language.
	for _, f := range files {
		if strings.EqualFold(f.Architecture, "ARM64") &&
			strings.EqualFold(f.LanguageCode, langLower) {
			c.logf("found MCT ESD (relaxed match): %s (edition=%s)", f.FileName, f.Edition)
			return &f, nil
		}
	}

	return nil, fmt.Errorf("no ARM64 ESD found for lang=%s edition=%s (%d entries searched)", language, edition, len(files))
}

func (c *Client) latestCatalogURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, WorProjectURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var db struct {
		Versions struct {
			Version []struct {
				LatestCabLink string `xml:"latestCabLink"`
				Number        string `xml:"number,attr"`
			} `xml:"version"`
		} `xml:"versions"`
	}
	if err := xml.Unmarshal(body, &db); err != nil {
		return "", fmt.Errorf("parsing worproject XML: %w", err)
	}

	for _, v := range db.Versions.Version {
		if v.Number == "11" && v.LatestCabLink != "" {
			return v.LatestCabLink, nil
		}
	}
	return "", nil
}

func (c *Client) fetchAndExtractCatalog(ctx context.Context, catalogURL string) ([]byte, error) {
	c.logf("downloading MCT catalog CAB from %s", catalogURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, catalogURL)
	}

	cabData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading CAB: %w", err)
	}
	c.logf("downloaded CAB: %d bytes", len(cabData))

	files, err := extractCABWithFallback(cabData, c.logf)
	if err != nil {
		return nil, fmt.Errorf("extracting CAB: %w", err)
	}

	for name, data := range files {
		c.logf("  CAB entry: %s (%d bytes)", name, len(data))
		lower := strings.ToLower(name)
		if strings.Contains(lower, "products") && strings.HasSuffix(lower, ".xml") {
			return data, nil
		}
	}

	return nil, fmt.Errorf("no products*.xml found in CAB (entries: %d)", len(files))
}

type mctRoot struct {
	Catalogs struct {
		Catalog struct {
			PublishedMedia struct {
				Files struct {
					File []mctFile `xml:"File"`
				} `xml:"Files"`
			} `xml:"PublishedMedia"`
		} `xml:"Catalog"`
	} `xml:"Catalogs"`
}

type mctFile struct {
	FileName     string `xml:"FileName"`
	LanguageCode string `xml:"LanguageCode"`
	Language     string `xml:"Language"`
	Edition      string `xml:"Edition"`
	Architecture string `xml:"Architecture"`
	Size         string `xml:"Size"`
	SHA1         string `xml:"Sha1"`
	FilePath     string `xml:"FilePath"`
	IsRetailOnly string `xml:"IsRetailOnly"`
}

func parseProductsXML(data []byte) ([]ESDFile, error) {
	var root mctRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, err
	}

	rawFiles := root.Catalogs.Catalog.PublishedMedia.Files.File
	result := make([]ESDFile, 0, len(rawFiles))
	for _, f := range rawFiles {
		size, _ := strconv.ParseInt(f.Size, 10, 64)
		result = append(result, ESDFile{
			FileName:     f.FileName,
			LanguageCode: f.LanguageCode,
			Edition:      f.Edition,
			Architecture: f.Architecture,
			Size:         size,
			SHA1:         f.SHA1,
			FilePath:     f.FilePath,
		})
	}
	return result, nil
}
