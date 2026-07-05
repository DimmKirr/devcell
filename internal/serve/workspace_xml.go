package serve

import (
	"bytes"
	"encoding/xml"
	"text/template"
	"time"
)

const WorkspaceFeedContentType = "application/x-msts-radc+xml; charset=utf-8"

type Cell struct {
	ID         string
	Title      string
	Host       string
	Port       int
	Type       string // "Desktop" or "RemoteApp"
	AppProgram string // RemoteApp program alias (e.g. "||xterm")
}

// XML structs kept for test parsing only.

type ResourceCollection struct {
	XMLName           xml.Name  `xml:"http://schemas.microsoft.com/ts/2007/05/tswf ResourceCollection"`
	PubDate           string    `xml:"PubDate,attr"`
	SchemaVersion     string    `xml:"SchemaVersion,attr"`
	SupportsReconnect string    `xml:"SupportsReconnect,attr,omitempty"`
	Publisher         Publisher `xml:"Publisher"`
}

type Publisher struct {
	LastUpdated     string          `xml:"LastUpdated,attr"`
	Name            string          `xml:"Name,attr"`
	ID              string          `xml:"ID,attr"`
	Description     string          `xml:"Description,attr"`
	Resources       Resources       `xml:"Resources"`
	TerminalServers TerminalServers `xml:"TerminalServers"`
}

type Resources struct {
	Resource []Resource `xml:"Resource"`
}

type Resource struct {
	ID                     string                 `xml:"ID,attr"`
	Alias                  string                 `xml:"Alias,attr"`
	Title                  string                 `xml:"Title,attr"`
	LastUpdated            string                 `xml:"LastUpdated,attr"`
	Type                   string                 `xml:"Type,attr"`
	ShowByDefault          string                 `xml:"ShowByDefault,attr"`
	Icons                  Icons                  `xml:"Icons"`
	FileExtensions         string                 `xml:"FileExtensions"`
	Folders                Folders                `xml:"Folders"`
	HostingTerminalServers HostingTerminalServers `xml:"HostingTerminalServers"`
}

type Icons struct {
	IconRaw *IconElement `xml:"IconRaw,omitempty"`
	Icon32  *IconElement `xml:"Icon32,omitempty"`
	Icon256 *IconElement `xml:"Icon256,omitempty"`
}

type IconElement struct {
	Dimensions string `xml:"Dimensions,attr,omitempty"`
	FileType   string `xml:"FileType,attr"`
	FileURL    string `xml:"FileURL,attr"`
}

type Folders struct {
	Folder []Folder `xml:"Folder"`
}

type Folder struct {
	Name string `xml:"Name,attr"`
}

type HostingTerminalServers struct {
	HTS []HostingTerminalServer `xml:"HostingTerminalServer"`
}

type HostingTerminalServer struct {
	ResourceFile      ResourceFile      `xml:"ResourceFile"`
	TerminalServerRef TerminalServerRef `xml:"TerminalServerRef"`
}

type ResourceFile struct {
	FileExtension string `xml:"FileExtension,attr"`
	URL           string `xml:"URL,attr"`
}

type TerminalServerRef struct {
	Ref string `xml:"Ref,attr"`
}

type TerminalServers struct {
	TS []TerminalServer `xml:"TerminalServer"`
}

type TerminalServer struct {
	ID          string `xml:"ID,attr"`
	Name        string `xml:"Name,attr"`
	LastUpdated string `xml:"LastUpdated,attr"`
}

var xmlEscaper = template.FuncMap{
	"xmlattr": func(s string) string {
		var buf bytes.Buffer
		for _, r := range s {
			switch r {
			case '<':
				buf.WriteString("&lt;")
			case '>':
				buf.WriteString("&gt;")
			case '&':
				buf.WriteString("&amp;")
			case '"':
				buf.WriteString("&quot;")
			default:
				buf.WriteRune(r)
			}
		}
		return buf.String()
	},
}

// feedTmpl produces XML byte-identical to RAWeb's WorkspaceBuilder.cs output:
// self-closing tags, attribute order matching RAWeb, \r\n line endings.
var feedTmpl = template.Must(template.New("feed").Funcs(xmlEscaper).Parse(
	"<?xml version=\"1.0\" encoding=\"utf-8\"?>\r\n" +
		"<ResourceCollection PubDate=\"{{.PubDate}}\" SchemaVersion=\"2.1\" SupportsReconnect=\"false\" xmlns=\"http://schemas.microsoft.com/ts/2007/05/tswf\">\r\n" +
		"<Publisher LastUpdated=\"{{.PubDate}}\" Name=\"devcell\" ID=\"devcell-workspace\" Description=\"\">\r\n" +
		"<Resources>\r\n" +
		"{{range .Resources}}" +
		"<Resource ID=\"{{.ID}}\" Alias=\"{{.ID}}\" Title=\"{{.Title | xmlattr}}\" LastUpdated=\"{{$.PubDate}}\" Type=\"{{.Type}}\" ShowByDefault=\"True\">\r\n" +
		"<Icons>\r\n" +
		"<IconRaw FileType=\"Ico\" FileURL=\"https://{{$.BaseHost}}/icons/{{.ID}}.ico\" />\r\n" +
		"<Icon32 Dimensions=\"32x32\" FileType=\"Png\" FileURL=\"https://{{$.BaseHost}}/icons/{{.ID}}.png\" />\r\n" +
		"<Icon256 Dimensions=\"256x256\" FileType=\"Png\" FileURL=\"https://{{$.BaseHost}}/icons/{{.ID}}-256.png\" />\r\n" +
		"</Icons>\r\n" +
		"<FileExtensions />\r\n" +
		"<Folders><Folder Name=\"/\" /></Folders>\r\n" +
		"<HostingTerminalServers>\r\n" +
		"<HostingTerminalServer>\r\n" +
		"<ResourceFile FileExtension=\".rdp\" URL=\"https://{{$.BaseHost}}/rdp/{{.ID}}.rdp\" />\r\n" +
		"<TerminalServerRef Ref=\"ts-{{.ID}}\" />\r\n" +
		"</HostingTerminalServer>\r\n" +
		"</HostingTerminalServers>\r\n" +
		"</Resource>\r\n" +
		"{{end}}" +
		"</Resources>\r\n" +
		"<TerminalServers>\r\n" +
		"{{range .Resources}}" +
		"<TerminalServer ID=\"ts-{{.ID}}\" Name=\"{{.Host}}:{{.Port}}\" LastUpdated=\"{{$.PubDate}}\" />\r\n" +
		"{{end}}" +
		"</TerminalServers>\r\n" +
		"</Publisher>\r\n" +
		"</ResourceCollection>\r\n"))

type feedData struct {
	PubDate   string
	BaseHost  string
	Resources []Cell
}

func EncodeFeed(cells []Cell, baseHost string, now time.Time) ([]byte, error) {
	ts := now.UTC().Format("2006-01-02T15:04:05.0Z")

	var buf bytes.Buffer
	if err := feedTmpl.Execute(&buf, feedData{
		PubDate:   ts,
		BaseHost:  baseHost,
		Resources: cells,
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func MockResources() []Cell {
	return []Cell{
		{ID: "mock-desktop-1", Title: "Dev Desktop 1", Host: "localhost", Port: 13389, Type: "Desktop"},
		{ID: "mock-desktop-2", Title: "Dev Desktop 2", Host: "localhost", Port: 13390, Type: "Desktop"},
		{ID: "mock-desktop-3", Title: "Dev Desktop 3", Host: "localhost", Port: 13391, Type: "Desktop"},
		{ID: "mock-terminal", Title: "Terminal", Host: "localhost", Port: 13392, Type: "RemoteApp", AppProgram: "||xterm"},
	}
}
