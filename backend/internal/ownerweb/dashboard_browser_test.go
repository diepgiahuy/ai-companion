package ownerweb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type webDriverClient struct {
	baseURL   string
	sessionID string
	client    *http.Client
}

func TestOwnerHubBrowserKeyboardFocusAndResponsiveFlow(t *testing.T) {
	driver := findChromeDriver()
	if driver == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("ChromeDriver is required for the hosted Owner Hub browser oracle")
		}
		t.Skip("ChromeDriver is not installed; hosted CI owns this browser oracle")
	}

	server := httptest.NewServer(ownerHubBrowserFixture())
	defer server.Close()

	port := freeTCPPort(t)
	cmd := exec.Command(driver, fmt.Sprintf("--port=%d", port), "--url-base=/")
	var driverLog bytes.Buffer
	cmd.Stdout = &driverLog
	cmd.Stderr = &driverLog
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ChromeDriver: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	wd := &webDriverClient{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
	if err := waitForWebDriver(wd, 10*time.Second); err != nil {
		t.Fatalf("ChromeDriver did not become ready: %v\n%s", err, driverLog.String())
	}
	if err := wd.newSession(); err != nil {
		t.Fatalf("create Chrome session: %v\n%s", err, driverLog.String())
	}
	defer wd.closeSession()

	if _, err := wd.request(http.MethodPost, "/session/"+wd.sessionID+"/url", map[string]any{"url": server.URL + "/v1/owner/dashboard"}); err != nil {
		t.Fatalf("navigate Owner Hub: %v", err)
	}
	waitForScript(t, wd, `return document.readyState === 'complete' && !!document.querySelector('[data-create="expense"]');`)

	// Prove that the primary navigation and Quick Add are reachable by keyboard only.
	wanted := []string{"Home", "Companion", "Personal", "Settings", "Sign out", "+ ExpenseExisting personal domain"}
	seen := make([]string, 0, len(wanted))
	for i := 0; i < 14 && len(seen) < len(wanted); i++ {
		pressKey(t, wd, "\ue004") // Tab
		active := scriptMap(t, wd, `const e=document.activeElement; const s=getComputedStyle(e); const r=e.getBoundingClientRect(); return {text:(e.textContent||'').trim(), tag:e.tagName, outline:s.outlineStyle, outlineWidth:s.outlineWidth, top:r.top, bottom:r.bottom, viewport:innerHeight};`)
		text, _ := active["text"].(string)
		if text == wanted[len(seen)] {
			if active["tag"] != "BUTTON" {
				t.Fatalf("keyboard target %q is not a button: %#v", text, active)
			}
			if active["outline"] == "none" || active["outlineWidth"] == "0px" {
				t.Fatalf("keyboard target %q has no visible focus outline: %#v", text, active)
			}
			if asFloat(active["top"]) < 0 || asFloat(active["bottom"]) > asFloat(active["viewport"])+1 {
				t.Fatalf("focused control %q is obscured outside the viewport: %#v", text, active)
			}
			seen = append(seen, text)
		}
	}
	if len(seen) != len(wanted) {
		t.Fatalf("keyboard traversal did not reach expected controls in order: got=%v want=%v", seen, wanted)
	}

	pressKey(t, wd, "\ue007") // Enter on Quick Add Expense.
	waitForScript(t, wd, `return document.getElementById('edit-sheet').open && document.activeElement.id === 'sheet-amount_vnd';`)
	pressKey(t, wd, "\ue00c") // Escape.
	waitForScript(t, wd, `return !document.getElementById('edit-sheet').open && document.activeElement?.dataset?.create === 'expense';`)

	// Open an existing expense and prove destructive confirmation identifies the record.
	executeScript(t, wd, `openPersonal('money'); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('[data-edit-expense="1"]');`)
	executeScript(t, wd, `document.querySelector('[data-edit-expense="1"]').click(); return true;`)
	waitForScript(t, wd, `return document.getElementById('edit-sheet').open && document.activeElement.id === 'sheet-amount_vnd';`)
	executeScript(t, wd, `document.getElementById('sheet-delete').click(); return true;`)
	waitForScript(t, wd, `return document.getElementById('confirm-dialog').open;`)
	confirmText := scriptString(t, wd, `return document.getElementById('confirm-text').textContent;`)
	if !strings.Contains(confirmText, "Lunch") && !strings.Contains(confirmText, "125,000") {
		t.Fatalf("destructive confirmation does not identify the expense: %q", confirmText)
	}
	pressKey(t, wd, "\ue00c")
	waitForScript(t, wd, `return !document.getElementById('confirm-dialog').open && document.activeElement.id === 'sheet-delete';`)
	pressKey(t, wd, "\ue00c")
	waitForScript(t, wd, `return !document.getElementById('edit-sheet').open && document.activeElement?.dataset?.editExpense === '1';`)

	// Prove the responsive shell does not horizontally overflow and the edit sheet becomes a bottom sheet.
	if _, err := wd.request(http.MethodPost, "/session/"+wd.sessionID+"/window/rect", map[string]any{"width": 390, "height": 844}); err != nil {
		t.Fatalf("set mobile viewport: %v", err)
	}
	executeScript(t, wd, `app.view='home'; render(); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('[data-create="expense"]');`)
	mobile := scriptMap(t, wd, `const nav=document.querySelector('.mobile-nav'); const sidebar=document.querySelector('.sidebar'); return {noOverflow:document.documentElement.scrollWidth<=document.documentElement.clientWidth, mobileDisplay:getComputedStyle(nav).display, sidebarDisplay:getComputedStyle(sidebar).display};`)
	if mobile["noOverflow"] != true || mobile["mobileDisplay"] == "none" || mobile["sidebarDisplay"] != "none" {
		t.Fatalf("mobile responsive contract failed: %#v", mobile)
	}
	executeScript(t, wd, `document.querySelector('[data-create="expense"]').click(); return true;`)
	waitForScript(t, wd, `return document.getElementById('edit-sheet').open && document.activeElement.id === 'sheet-amount_vnd';`)
	bottomSheet := scriptMap(t, wd, `const r=document.getElementById('edit-sheet').getBoundingClientRect(); return {bottom:r.bottom,width:r.width,viewportHeight:innerHeight,viewportWidth:innerWidth};`)
	if absFloat(asFloat(bottomSheet["bottom"])-asFloat(bottomSheet["viewportHeight"])) > 3 {
		t.Fatalf("mobile edit sheet is not anchored to viewport bottom: %#v", bottomSheet)
	}
	if absFloat(asFloat(bottomSheet["width"])-asFloat(bottomSheet["viewportWidth"])) > 3 {
		t.Fatalf("mobile edit sheet does not fill viewport width: %#v", bottomSheet)
	}
}

func ownerHubBrowserFixture() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/owner/dashboard", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, dashboardHTML)
	})
	mux.HandleFunc("/v1/owner/data/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		switch r.URL.Path {
		case "/v1/owner/data/devices":
			_, _ = io.WriteString(w, `{"devices":[]}`)
		case "/v1/owner/data/device":
			_, _ = io.WriteString(w, `{"has_device":false,"device_id":"","connection_status":"offline","desired":{},"reported":{},"desired_version":0,"reported_version":0,"settings_status":{"state":"unknown"}}`)
		case "/v1/owner/data/overview":
			_, _ = io.WriteString(w, `{"month_total":125000,"monthly_budget":0,"budget_set":false,"expenses":[],"notes":[],"voice_memos":[],"reminders":[]}`)
		case "/v1/owner/data/expenses":
			_, _ = io.WriteString(w, `{"total_vnd":125000,"expenses":[{"id":1,"amount_vnd":125000,"category":"food","description":"Lunch","occurred_at":"2026-08-21T05:00:00Z"}]}`)
		case "/v1/owner/data/savings-goal":
			_, _ = io.WriteString(w, `{"set":false}`)
		case "/v1/owner/data/notes":
			_, _ = io.WriteString(w, `{"notes":[]}`)
		case "/v1/owner/data/journal":
			_, _ = io.WriteString(w, `{"journal":[]}`)
		case "/v1/owner/data/reminders":
			_, _ = io.WriteString(w, `{"reminders":[],"timers":[]}`)
		case "/v1/owner/data/voice-memos":
			_, _ = io.WriteString(w, `{"voice_memos":[]}`)
		case "/v1/owner/data/privacy":
			_, _ = io.WriteString(w, `{"privacy":{"save_voice_audio":false,"voice_mail_policy":"disabled","long_term_memory_enabled":false,"conversation_retention_days":30,"voice_memo_retention_days":30,"memory_retention_days":90}}`)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func findChromeDriver() string {
	if path, err := exec.LookPath("chromedriver"); err == nil {
		return path
	}
	if root := strings.TrimSpace(os.Getenv("CHROMEWEBDRIVER")); root != "" {
		candidate := filepath.Join(root, "chromedriver")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	for _, candidate := range []string{"/usr/local/share/chromedriver-linux64/chromedriver", "/usr/bin/chromedriver"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve ChromeDriver port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForWebDriver(wd *webDriverClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := wd.client.Get(wd.baseURL + "/status")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

func (wd *webDriverClient) newSession() error {
	value, err := wd.request(http.MethodPost, "/session", map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{
				"browserName": "chrome",
				"goog:chromeOptions": map[string]any{
					"args": []string{"--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--window-size=1280,900"},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	if id, ok := value["sessionId"].(string); ok && id != "" {
		wd.sessionID = id
		return nil
	}
	return fmt.Errorf("ChromeDriver session response missing sessionId: %#v", value)
}

func (wd *webDriverClient) closeSession() {
	if wd.sessionID != "" {
		_, _ = wd.request(http.MethodDelete, "/session/"+wd.sessionID, nil)
	}
}

func (wd *webDriverClient) request(method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, wd.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := wd.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Value any `json:"value"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("decode WebDriver response %s: %w", payload, err)
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("WebDriver %s %s returned %d: %s", method, path, response.StatusCode, payload)
	}
	if envelope.Value == nil {
		return map[string]any{}, nil
	}
	value, ok := envelope.Value.(map[string]any)
	if !ok {
		return map[string]any{"value": envelope.Value}, nil
	}
	return value, nil
}

func executeScript(t *testing.T, wd *webDriverClient, script string) any {
	t.Helper()
	value, err := wd.request(http.MethodPost, "/session/"+wd.sessionID+"/execute/sync", map[string]any{"script": script, "args": []any{}})
	if err != nil {
		t.Fatalf("execute browser script: %v", err)
	}
	return value["value"]
}

func waitForScript(t *testing.T, wd *webDriverClient, script string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if result, _ := executeScript(t, wd, script).(bool); result {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("browser condition timed out: %s", script)
}

func scriptMap(t *testing.T, wd *webDriverClient, script string) map[string]any {
	t.Helper()
	value, ok := executeScript(t, wd, script).(map[string]any)
	if !ok {
		t.Fatalf("browser script did not return an object: %s", script)
	}
	return value
}

func scriptString(t *testing.T, wd *webDriverClient, script string) string {
	t.Helper()
	value, ok := executeScript(t, wd, script).(string)
	if !ok {
		t.Fatalf("browser script did not return a string: %s", script)
	}
	return value
}

func pressKey(t *testing.T, wd *webDriverClient, key string) {
	t.Helper()
	_, err := wd.request(http.MethodPost, "/session/"+wd.sessionID+"/actions", map[string]any{
		"actions": []map[string]any{{
			"type": "key",
			"id":   "keyboard",
			"actions": []map[string]any{
				{"type": "keyDown", "value": key},
				{"type": "keyUp", "value": key},
			},
		}},
	})
	if err != nil {
		t.Fatalf("send browser key %q: %v", key, err)
	}
}

func asFloat(value any) float64 {
	number, _ := value.(float64)
	return number
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
