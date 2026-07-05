package serve

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"os/user"
	"strings"
	"time"
)

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func WorkspaceRoutes(enum CellEnumerator, publicHost string, opts ...WorkspaceOpt) *http.ServeMux {
	var cfg workspaceCfg
	for _, o := range opts {
		o(&cfg)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/RDWeb/Feed/webfeed.aspx", http.StatusFound)
	})

	if cfg.certDER != nil {
		pemBlock := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cfg.certDER})
		mux.HandleFunc("/cert", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-pem-file")
			w.Header().Set("Content-Disposition", "attachment; filename=devcell-ca.pem")
			w.Write(pemBlock)
		})
	}

	mux.Handle("/api/feeddiscovery/webfeeddiscovery.aspx", newFeedDiscoveryHandler(publicHost))
	mux.Handle("/RDWeb/Pages/en-US/login.aspx", newLoginHandler(publicHost))
	mux.Handle("/RDWeb/Feed/WebFeedLogin.aspx", newWebFeedLoginHandler())

	feedHandler := newFeedHandler(enum, publicHost)
	mux.Handle("/RDWeb/Feed/webfeed.aspx", requireWorkspaceAuth(feedHandler))
	mux.Handle("/webfeed.aspx", requireWorkspaceAuth(feedHandler))
	mux.Handle("/RDWeb/Feed/RDWebService.asmx", requireASPXAuth(NewSOAPReconnectStub()))
	mux.Handle("/rdp/", requireASPXAuth(newRDPFileHandler(enum, publicHost)))
	mux.Handle("/icons/", newIconHandler(enum))
	mux.Handle("/preview/", newPreviewHandler(enum))

	return mux
}

func newFeedHandler(enum CellEnumerator, publicHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		baseURL := resolveHostWithPort(publicHost, r)
		cells := enum.ListCells()
		body, err := EncodeFeed(cells, baseURL, time.Now())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		ct := "text/xml; charset=utf-8"
		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "radc_schema_version") {
			ct = WorkspaceFeedContentType
		}
		w.Header().Set("Content-Type", ct)
		w.Write(body)
	})
}

func newRDPFileHandler(enum CellEnumerator, publicHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/rdp/")
		name = strings.TrimSuffix(name, ".rdp")
		if name == "" {
			http.NotFound(w, r)
			return
		}

		cells := enum.ListCells()
		var found *Cell
		for i := range cells {
			if cells[i].ID == name {
				found = &cells[i]
				break
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}

		host := resolveHostname(publicHost, r)
		body := BuildRDPFile(host, found.Port, currentUser(), found.AppProgram)

		w.Header().Set("Content-Type", "application/x-rdp")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.rdp", found.ID))
		w.Write([]byte(body))
	})
}

const aspxAuthCookie = ".ASPXAUTH"

func requireASPXAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(aspxAuthCookie); err != nil {
			returnURL := r.URL.Path
			http.Redirect(w, r, "/RDWeb/Feed/WebFeedLogin.aspx?ReturnUrl="+returnURL, http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireWorkspaceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(aspxAuthCookie); err == nil {
			next.ServeHTTP(w, r)
			return
		}
		if isSubscriptionRequest(r) {
			returnURL := r.URL.Path
			http.Redirect(w, r, "/RDWeb/Feed/WebFeedLogin.aspx?ReturnUrl="+returnURL, http.StatusFound)
			return
		}
		w.Header().Set("WWW-Authenticate", "Negotiate")
		w.WriteHeader(http.StatusUnauthorized)
	})
}

func isSubscriptionRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("User-Agent"), "TSWorkspace") ||
		strings.Contains(r.Header.Get("Accept"), "radc_schema_version")
}

func newWebFeedLoginHandler() http.Handler {
	const cookieValue = "authenticated"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     aspxAuthCookie,
			Value:    cookieValue,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
		})
		w.Header().Set("Content-Type", "application/x-msts-webfeed-login; charset=utf-8")
		w.Header().Set("Persistent-Auth", "true")
		w.Write([]byte(cookieValue))
	})
}

const loginPageHTML = `<!DOCTYPE html>
<html>
<head><title>RemoteApp and Desktop Connection</title></head>
<body>
<form id="FrmLogin" name="FrmLogin" action="%s" method="POST">
<input type="hidden" name="WorkSpaceID" value="RDWeb" />
<input type="hidden" name="RDPCertificates" value="" />
<input type="hidden" name="PublicModeTimeout" value="20" />
<input type="hidden" name="PrivateModeTimeout" value="240" />
<input type="hidden" name="EventLogUploadAddress" value="" />
<input type="hidden" name="MachineType" value="private" />
<label for="DomainUserName">Domain\user name:</label>
<input type="text" name="DomainUserName" id="DomainUserName" value="" />
<label for="UserPass">Password:</label>
<input type="password" name="UserPass" id="UserPass" value="" />
<input type="submit" name="Sign+in" value="Sign in" />
</form>
</body>
</html>`

func newLoginHandler(publicHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			returnURL := r.URL.Query().Get("ReturnUrl")
			if returnURL == "" {
				returnURL = "/RDWeb/Feed/webfeed.aspx"
			}
			host := resolveHostWithPort(publicHost, r)
			scheme := "https"
			if r.TLS == nil {
				scheme = "http"
			}
			actionURL := fmt.Sprintf("%s://%s/RDWeb/Pages/en-US/login.aspx?ReturnUrl=%s", scheme, host, returnURL)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, loginPageHTML, actionURL)
			return
		}
		if r.Method == http.MethodPost {
			http.SetCookie(w, &http.Cookie{
				Name:     aspxAuthCookie,
				Value:    "authenticated",
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
			})
			returnURL := r.URL.Query().Get("ReturnUrl")
			if returnURL == "" {
				returnURL = "/RDWeb/Feed/webfeed.aspx"
			}
			http.Redirect(w, r, returnURL, http.StatusFound)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
}

type workspaceCfg struct {
	certDER []byte
}

type WorkspaceOpt func(*workspaceCfg)

func WithCertDER(der []byte) WorkspaceOpt {
	return func(c *workspaceCfg) { c.certDER = der }
}

func resolveHostname(configured string, r *http.Request) string {
	host := configured
	if host == "" {
		host = r.Host
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}
