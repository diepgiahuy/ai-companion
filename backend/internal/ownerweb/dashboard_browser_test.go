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
	cmd := exec.Command(driver, fmt.Sprintf("--port=%d", port))
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
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	if err := waitForWebDriver(wd, 10*time.Second); err != nil {
		t.Fatalf("ChromeDriver did not become ready: %v\n%s", err, driverLog.String())
	}
	if err := wd.newSession(); err != nil {
		t.Fatalf("create Chrome session: %v\n%s", err, driverLog.String())
	}
	defer wd.closeSession()

	wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/url", map[string]any{"url": server.URL + "/v1/owner/dashboard"})
	waitForScript(t, wd, `return document.readyState === 'complete' && !!document.querySelector('[data-create="expense"]');`)

	// Primary navigation and Quick Add must be reachable with Tab and show visible focus.
	wanted := []string{"Home", "Companion", "Personal", "Settings", "Sign out", "+ ExpenseExisting personal domain"}
	seen := make([]string, 0, len(wanted))
	for i := 0; i < 14 && len(seen) < len(wanted); i++ {
		pressKey(t, wd, "\ue004")
		active := scriptMap(t, wd, `const e=document.activeElement,s=getComputedStyle(e),r=e.getBoundingClientRect(); return {text:(e.textContent||'').trim(),tag:e.tagName,outline:s.outlineStyle,outlineWidth:s.outlineWidth,top:r.top,bottom:r.bottom,viewport:innerHeight};`)
		text, _ := active["text"].(string)
		if text != wanted[len(seen)] {
			continue
		}
		if active["tag"] != "BUTTON" || active["outline"] == "none" || active["outlineWidth"] == "0px" {
			t.Fatalf("keyboard target %q lacks semantic/visible focus: %#v", text, active)
		}
		if asFloat(active["top"]) < 0 || asFloat(active["bottom"]) > asFloat(active["viewport"])+1 {
			t.Fatalf("focused control %q is obscured: %#v", text, active)
		}
		seen = append(seen, text)
	}
	if len(seen) != len(wanted) {
		t.Fatalf("keyboard traversal got=%v want=%v", seen, wanted)
	}

	// Enter opens the edit sheet. Escape closes it and returns focus to the trigger.
	pressKey(t, wd, "\ue007")
	waitForScript(t, wd, `return document.getElementById('edit-sheet').open && document.activeElement.id === 'sheet-amount_vnd';`)
	pressKey(t, wd, "\ue00c")
	waitForScript(t, wd, `return !document.getElementById('edit-sheet').open && document.activeElement?.dataset?.create === 'expense';`)

	// Destructive confirmation must identify the affected record and restore focus on keyboard cancel.
	executeScript(t, wd, `openPersonal('money'); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('[data-edit-expense="1"]');`)
	executeScript(t, wd, `document.querySelector('[data-edit-expense="1"]').click(); return true;`)
	waitForScript(t, wd, `return document.getElementById('edit-sheet').open && document.activeElement.id === 'sheet-amount_vnd';`)
	executeScript(t, wd, `const trigger=document.getElementById('sheet-delete'); trigger.focus(); trigger.click(); return true;`)
	waitForScript(t, wd, `return document.getElementById('confirm-dialog').open && document.activeElement.id === 'confirm-ok';`)
	confirmText := scriptString(t, wd, `return document.getElementById('confirm-text').textContent;`)
	if !strings.Contains(confirmText, "Lunch") && !strings.Contains(confirmText, "125") {
		t.Fatalf("destructive confirmation does not identify expense: %q", confirmText)
	}
	pressShiftTab(t, wd)
	waitForScript(t, wd, `return document.activeElement.id === 'confirm-cancel';`)
	pressKey(t, wd, "\ue007")
	waitForScript(t, wd, `return !document.getElementById('confirm-dialog').open && document.activeElement.id === 'sheet-delete';`)
	pressKey(t, wd, "\ue00c")
	waitForScript(t, wd, `return !document.getElementById('edit-sheet').open && document.activeElement?.dataset?.editExpense === '1';`)

	// Mobile layout must avoid horizontal overflow and use the approved bottom-sheet pattern.
	wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/window/rect", map[string]any{"width": 390, "height": 844})
	executeScript(t, wd, `app.view='home'; render(); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('[data-create="expense"]');`)
	mobile := scriptMap(t, wd, `const nav=document.querySelector('.mobile-nav'),sidebar=document.querySelector('.sidebar'); return {noOverflow:document.documentElement.scrollWidth<=document.documentElement.clientWidth,mobileDisplay:getComputedStyle(nav).display,sidebarDisplay:getComputedStyle(sidebar).display};`)
	if mobile["noOverflow"] != true || mobile["mobileDisplay"] == "none" || mobile["sidebarDisplay"] != "none" {
		t.Fatalf("mobile responsive contract failed: %#v", mobile)
	}
	executeScript(t, wd, `document.querySelector('[data-create="expense"]').click(); return true;`)
	waitForScript(t, wd, `return document.getElementById('edit-sheet').open && document.activeElement.id === 'sheet-amount_vnd';`)
	bottom := scriptMap(t, wd, `const sheet=document.getElementById('edit-sheet'),r=sheet.getBoundingClientRect(); return {bottom:r.bottom,width:r.width,viewportHeight:innerHeight,viewportWidth:innerWidth,noOverflow:sheet.scrollWidth<=sheet.clientWidth};`)
	if absFloat(asFloat(bottom["bottom"])-asFloat(bottom["viewportHeight"])) > 3 || asFloat(bottom["width"]) < asFloat(bottom["viewportWidth"])*0.9 || bottom["noOverflow"] != true {
		t.Fatalf("mobile edit sheet violates bottom-sheet responsive contract: %#v", bottom)
	}
}

func ownerHubBrowserFixture() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/owner/dashboard", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, dashboardHTML)
	})
	responses := map[string]string{
		"/v1/owner/data/devices":      `{"devices":[]}`,
		"/v1/owner/data/device":       `{"has_device":false,"device_id":"","connection_status":"offline","desired":{},"reported":{},"desired_version":0,"reported_version":0,"settings_status":{"state":"unknown"}}`,
		"/v1/owner/data/overview":     `{"month_total":125000,"monthly_budget":0,"budget_set":false,"expenses":[],"notes":[],"voice_memos":[],"reminders":[]}`,
		"/v1/owner/data/expenses":     `{"total_vnd":125000,"expenses":[{"id":1,"amount_vnd":125000,"category":"food","description":"Lunch","occurred_at":"2026-08-21T05:00:00Z"}]}`,
		"/v1/owner/data/savings-goal": `{"set":false}`,
		"/v1/owner/data/notes":        `{"notes":[]}`,
		"/v1/owner/data/journal":      `{"journal":[]}`,
		"/v1/owner/data/reminders":    `{"reminders":[],"timers":[]}`,
		"/v1/owner/data/voice-memos":  `{"voice_memos":[]}`,
		"/v1/owner/data/privacy":      `{"privacy":{"save_voice_audio":false,"voice_mail_policy":"disabled","long_term_memory_enabled":false,"conversation_retention_days":30,"voice_memo_retention_days":30,"memory_retention_days":90}}`,
	}
	mux.HandleFunc("/v1/owner/data/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		if payload, ok := responses[r.URL.Path]; ok {
			_, _ = io.WriteString(w, payload)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

func findChromeDriver() string {
	if path, err := exec.LookPath("chromedriver"); err == nil {
		return path
	}
	if root := strings.TrimSpace(os.Getenv("CHROMEWEBDRIVER")); root != "" {
		if candidate := filepath.Join(root, "chromedriver"); fileExists(candidate) {
			return candidate
		}
	}
	for _, candidate := range []string{"/usr/local/share/chromedriver-linux64/chromedriver", "/usr/bin/chromedriver"} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
	value, err := wd.do(http.MethodPost, "/session", map[string]any{
		"capabilities": map[string]any{"alwaysMatch": map[string]any{
			"browserName": "chrome",
			"goog:chromeOptions": map[string]any{"args": []string{
				"--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--window-size=1280,900",
			}},
		}},
	})
	if err != nil {
		return err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected session response: %#v", value)
	}
	id, _ := object["sessionId"].(string)
	if id == "" {
		return fmt.Errorf("session response missing sessionId: %#v", object)
	}
	wd.sessionID = id
	return nil
}

func (wd *webDriverClient) closeSession() {
	if wd.sessionID != "" {
		_, _ = wd.do(http.MethodDelete, "/session/"+wd.sessionID, nil)
	}
}

func (wd *webDriverClient) must(t *testing.T, method, path string, body any) any {
	t.Helper()
	value, err := wd.do(method, path, body)
	if err != nil {
		t.Fatalf("WebDriver %s %s: %v", method, path, err)
	}
	return value
}

func (wd *webDriverClient) do(method, path string, body any) (any, error) {
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
			return nil, fmt.Errorf("decode WebDriver response: %w", err)
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("status=%d payload=%s", response.StatusCode, payload)
	}
	return envelope.Value, nil
}

func executeScript(t *testing.T, wd *webDriverClient, script string) any {
	t.Helper()
	return wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/execute/sync", map[string]any{"script": script, "args": []any{}})
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
		t.Fatalf("browser script did not return object: %s", script)
	}
	return value
}

func scriptString(t *testing.T, wd *webDriverClient, script string) string {
	t.Helper()
	value, ok := executeScript(t, wd, script).(string)
	if !ok {
		t.Fatalf("browser script did not return string: %s", script)
	}
	return value
}

func pressKey(t *testing.T, wd *webDriverClient, key string) {
	t.Helper()
	wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/actions", map[string]any{
		"actions": []map[string]any{{
			"type": "key", "id": "keyboard",
			"actions": []map[string]any{{"type": "keyDown", "value": key}, {"type": "keyUp", "value": key}},
		}},
	})
}

func pressShiftTab(t *testing.T, wd *webDriverClient) {
	t.Helper()
	wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/actions", map[string]any{
		"actions": []map[string]any{{
			"type": "key", "id": "keyboard",
			"actions": []map[string]any{
				{"type": "keyDown", "value": "\ue008"},
				{"type": "keyDown", "value": "\ue004"},
				{"type": "keyUp", "value": "\ue004"},
				{"type": "keyUp", "value": "\ue008"},
			},
		}},
	})
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
