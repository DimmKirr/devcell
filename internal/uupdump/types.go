package uupdump

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type APIError struct {
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("uupdump API error: %s", e.Message)
}

type responseEnvelope struct {
	Response json.RawMessage `json:"response"`
}

func (e *responseEnvelope) checkError() error {
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(e.Response, &errResp); err == nil && errResp.Error != "" {
		return &APIError{Message: errResp.Error}
	}
	return nil
}

type Build struct {
	Title   string `json:"title"`
	Build   string `json:"build"`
	Arch    string `json:"arch"`
	Created int64  `json:"created"`
	UUID    string `json:"uuid"`
}

type BuildsResponse struct {
	APIVersion string  `json:"apiVersion"`
	Builds     []Build `json:"-"`
}

func (br *BuildsResponse) UnmarshalJSON(data []byte) error {
	type plain struct {
		APIVersion string          `json:"apiVersion"`
		Builds     json.RawMessage `json:"builds"`
	}

	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	br.APIVersion = p.APIVersion

	if len(p.Builds) == 0 {
		return nil
	}

	switch p.Builds[0] {
	case '[':
		return json.Unmarshal(p.Builds, &br.Builds)
	case '{':
		var m map[string]Build
		if err := json.Unmarshal(p.Builds, &m); err != nil {
			return err
		}
		br.Builds = make([]Build, 0, len(m))
		for _, b := range m {
			br.Builds = append(br.Builds, b)
		}
		return nil
	default:
		return fmt.Errorf("unexpected builds type: %c", p.Builds[0])
	}
}

type DetailsResponse struct {
	APIVersion string   `json:"apiVersion"`
	UpdateName string   `json:"updateName"`
	Arch       string   `json:"arch"`
	Build      string   `json:"build"`
	LangList   []string `json:"langList"`
}

type EditionsResponse struct {
	APIVersion    string   `json:"apiVersion"`
	UpdateName    string   `json:"updateName"`
	Arch          string   `json:"arch"`
	Build         string   `json:"build"`
	EditionList   []string `json:"editionList"`
	LangFancyName string   `json:"langFancyName"`
}

type File struct {
	SHA1   string `json:"sha1"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"-"`
	URL    string `json:"url"`
	UUID   string `json:"uuid"`
	Expire int64  `json:"expire"`
	Debug  string `json:"debug,omitempty"`
}

func (f *File) UnmarshalJSON(data []byte) error {
	type plain File
	type withSize struct {
		plain
		Size json.RawMessage `json:"size"`
	}

	var ws withSize
	if err := json.Unmarshal(data, &ws); err != nil {
		return err
	}

	*f = File(ws.plain)

	if len(ws.Size) == 0 {
		return nil
	}

	if ws.Size[0] == '"' {
		var s string
		if err := json.Unmarshal(ws.Size, &s); err != nil {
			return err
		}
		n, _ := strconv.ParseInt(s, 10, 64)
		f.Size = n
	} else {
		var n int64
		if err := json.Unmarshal(ws.Size, &n); err != nil {
			return err
		}
		f.Size = n
	}

	return nil
}

type PackageResponse struct {
	APIVersion string          `json:"apiVersion"`
	UpdateName string          `json:"updateName"`
	Arch       string          `json:"arch"`
	Build      string          `json:"build"`
	SKU        int             `json:"sku"`
	HasUpdates bool            `json:"hasUpdates"`
	Files      map[string]File `json:"files"`
}
