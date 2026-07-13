package tool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
	playwright "github.com/mxschmitt/playwright-go"
)

func ProfileDir(name string) (string, error) {
	if name == "" {
		name = "default"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ag", "profiles", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// CookiesFile returns the path to the plain-JSON cookies file for a profile.
func CookiesFile(profileDir string) string {
	return filepath.Join(profileDir, "ag-cookies.json")
}

// ExportCookies opens loginProfileDir with Playwright (which can read the
// unencrypted cookie store written by Chrome on that profile), extracts all
// cookies for the given URL, and saves them as plain JSON to
// profileDir/ag-cookies.json so subsequent Playwright calls can load them
// without needing Keychain access.
func ExportCookies(loginProfileDir, profileDir, siteURL string) error {
	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("playwright: %w", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.LaunchPersistentContext(loginProfileDir, playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(true),
		Args:     chromeArgs(),
	})
	if err != nil {
		return fmt.Errorf("browser: %w", err)
	}
	defer browser.Close()

	cookies, err := browser.Cookies(siteURL)
	if err != nil {
		return fmt.Errorf("reading cookies: %w", err)
	}

	data, err := json.MarshalIndent(cookies, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling cookies: %w", err)
	}

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("creating profile dir: %w", err)
	}

	if err := os.WriteFile(CookiesFile(profileDir), data, 0o600); err != nil {
		return fmt.Errorf("writing cookies file: %w", err)
	}

	return nil
}

// ExportCookiesViaCDP connects to a running Chrome instance via its remote
// debugging port, calls the Network.getAllCookies CDP command (which returns
// decrypted cookies because Chrome itself holds the Keychain key), and saves
// them as plain JSON to profileDir/ag-cookies.json.
func ExportCookiesViaCDP(debugPort, profileDir string) error {
	// Retry for up to 5 seconds — Chrome needs a moment to start listening.
	// Use /json/version for the browser-level WebSocket (gives access to ALL cookies
	// across all tabs and domains, not just the current tab's cookies).
	var wsURL string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://localhost:" + debugPort + "/json/version")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var info struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			if json.Unmarshal(body, &info) == nil && info.WebSocketDebuggerURL != "" {
				wsURL = info.WebSocketDebuggerURL
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if wsURL == "" {
		return fmt.Errorf("could not connect to Chrome debugger on port %s", debugPort)
	}

	// Network.getAllCookies on the browser target requires enabling it first via
	// a target. Find the first page target and use its session to call Storage.getCookies
	// which returns all cookies browser-wide.
	var pageTargetID string
	{
		resp, err := http.Get("http://localhost:" + debugPort + "/json")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var targets []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if json.Unmarshal(body, &targets) == nil {
				for _, t := range targets {
					if t.Type == "page" {
						pageTargetID = t.ID
						break
					}
				}
			}
		}
	}
	_ = pageTargetID // used below if needed

	// Connect directly via WebSocket to call Network.getAllCookies.
	// This returns all cookies Chrome holds, fully decrypted (Chrome owns the Keychain key).
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()

	// Send Network.getAllCookies CDP command.
	type cdpMsg struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}
	if err := conn.WriteJSON(cdpMsg{ID: 1, Method: "Network.getAllCookies"}); err != nil {
		return fmt.Errorf("CDP send: %w", err)
	}

	// Read response — may need to skip event messages before our reply.
	type cdpCookie struct {
		Name     string  `json:"name"`
		Value    string  `json:"value"`
		Domain   string  `json:"domain"`
		Path     string  `json:"path"`
		Expires  float64 `json:"expires"`
		HttpOnly bool    `json:"httpOnly"`
		Secure   bool    `json:"secure"`
		SameSite string  `json:"sameSite"`
	}
	type cdpReply struct {
		ID     int `json:"id"`
		Result struct {
			Cookies []cdpCookie `json:"cookies"`
		} `json:"result"`
	}

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		var reply cdpReply
		if err := conn.ReadJSON(&reply); err != nil {
			return fmt.Errorf("CDP read: %w", err)
		}
		if reply.ID == 1 {
			data, err := json.MarshalIndent(reply.Result.Cookies, "", "  ")
			if err != nil {
				return fmt.Errorf("marshalling cookies: %w", err)
			}
			if err := os.MkdirAll(profileDir, 0o700); err != nil {
				return fmt.Errorf("creating profile dir: %w", err)
			}
			return os.WriteFile(CookiesFile(profileDir), data, 0o600)
		}
	}
}

// LoginWithPlaywright opens the URL in a visible Playwright browser using the
// persistent profile so the user can log in. Blocks until the user closes the
// browser or presses Enter in the terminal. Cookies are written directly to
// the profile directory — no export step needed.
func LoginWithPlaywright(profileDir, url string) error {
	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("playwright: %w", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.LaunchPersistentContext(profileDir, playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(false),
		Args:     chromeArgs(),
	})
	if err != nil {
		return fmt.Errorf("browser: %w", err)
	}

	pages := browser.Pages()
	var page playwright.Page
	if len(pages) > 0 {
		page = pages[0]
	} else {
		page, err = browser.NewPage()
		if err != nil {
			browser.Close()
			return fmt.Errorf("new page: %w", err)
		}
	}

	if _, err := page.Goto(url); err != nil {
		browser.Close()
		return fmt.Errorf("navigate: %w", err)
	}

	// Wait for the user to finish logging in.
	fmt.Print("\nPress Enter when you have finished logging in... ")
	fmt.Scanln()

	// Close browser — Playwright flushes the persistent context to disk.
	browser.Close()
	return nil
}

// cdpCookieFile mirrors the CDP Network.getAllCookies response cookie format.
type cdpCookieFile struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HttpOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite"`
}

// LoadCookies reads the plain-JSON cookies file and adds them to the browser context.
func LoadCookies(browser playwright.BrowserContext, profileDir string) error {
	data, err := os.ReadFile(CookiesFile(profileDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no cookies saved yet — not an error
		}
		return fmt.Errorf("reading cookies file: %w", err)
	}

	var cookies []cdpCookieFile
	if err := json.Unmarshal(data, &cookies); err != nil {
		return fmt.Errorf("parsing cookies: %w", err)
	}

	optional := make([]playwright.OptionalCookie, len(cookies))
	for i, c := range cookies {
		domain := c.Domain
		path := c.Path
		expires := c.Expires
		httpOnly := c.HttpOnly
		secure := c.Secure
		optional[i] = playwright.OptionalCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   &domain,
			Path:     &path,
			Expires:  &expires,
			HttpOnly: &httpOnly,
			Secure:   &secure,
		}
		if c.SameSite != "" {
			ss := playwright.SameSiteAttribute(c.SameSite)
			optional[i].SameSite = &ss
		}
	}

	if err := browser.AddCookies(optional); err != nil {
		return fmt.Errorf("adding cookies: %w", err)
	}

	return nil
}
