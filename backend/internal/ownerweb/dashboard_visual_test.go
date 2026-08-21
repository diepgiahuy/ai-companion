package ownerweb

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestOwnerHubVisualContract is intentionally opt-in. The dedicated Figma visual
// workflow owns this external visual oracle; ordinary Go/package tests keep the
// existing deterministic behavior/browser coverage without requiring Figma.
func TestOwnerHubVisualContract(t *testing.T) {
	outPath := os.Getenv("OWNER_VISUAL_CONTRACT_OUT")
	if outPath == "" {
		t.Skip("dedicated Owner Hub visual workflow owns this oracle")
	}
	screenshotDir := os.Getenv("OWNER_VISUAL_SCREENSHOT_DIR")
	if screenshotDir == "" {
		screenshotDir = filepath.Join(filepath.Dir(outPath), "screenshots")
	}
	if err := os.MkdirAll(screenshotDir, 0o755); err != nil {
		t.Fatalf("create screenshot directory: %v", err)
	}

	driver := findChromeDriver()
	if driver == "" {
		t.Fatal("ChromeDriver is required for the Owner Hub visual oracle")
	}
	server := httptest.NewServer(ownerHubVisualFixture())
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

	wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/window/rect", map[string]any{"width": 1120, "height": 720})
	wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/url", map[string]any{"url": server.URL + "/v1/owner/dashboard"})
	waitForScript(t, wd, `return document.readyState === 'complete' && !!document.querySelector('[data-create="expense"]');`)

	contract := map[string]any{"desktop": captureDesktopVisualContract(t, wd)}
	captureScreenshot(t, wd, filepath.Join(screenshotDir, "home-desktop.png"))

	executeScript(t, wd, `setView('companion'); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('#wake-model') && !!document.querySelector('.state-card');`)
	contract["companion"] = captureCompanionVisualContract(t, wd)
	captureScreenshot(t, wd, filepath.Join(screenshotDir, "companion-desktop.png"))

	executeScript(t, wd, `setView('personal'); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('#personal-body');`)
	captureScreenshot(t, wd, filepath.Join(screenshotDir, "personal-desktop.png"))

	executeScript(t, wd, `setView('settings'); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('#save-privacy');`)
	captureScreenshot(t, wd, filepath.Join(screenshotDir, "settings-desktop.png"))

	wd.must(t, http.MethodPost, "/session/"+wd.sessionID+"/window/rect", map[string]any{"width": 360, "height": 360})
	executeScript(t, wd, `setView('home'); return true;`)
	waitForScript(t, wd, `return !!document.querySelector('[data-create="expense"]');`)
	contract["mobile"] = captureMobileVisualContract(t, wd)
	captureScreenshot(t, wd, filepath.Join(screenshotDir, "home-mobile.png"))

	encoded, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		t.Fatalf("marshal visual contract: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatalf("create visual contract directory: %v", err)
	}
	if err := os.WriteFile(outPath, append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("write visual contract: %v", err)
	}
}

func captureDesktopVisualContract(t *testing.T, wd *webDriverClient) map[string]any {
	t.Helper()
	return scriptMap(t, wd, `
const q=s=>document.querySelector(s), cs=e=>getComputedStyle(e), num=v=>parseFloat(v)||0;
const rect=e=>e.getBoundingClientRect();
const rel=(e,b)=>{const r=rect(e),p=rect(b);return {x:r.x-p.x,y:r.y-p.y,width:r.width,height:r.height}};
const sidebar=q('.sidebar'), brand=q('.brand'), active=q('.nav button.active'), inactive=[...document.querySelectorAll('.nav button')].find(e=>!e.classList.contains('active'));
const content=q('.content'), h1=q('.head h1'), lead=q('.lead'), live=q('.live'), device=q('.device-summary'), quick=q('.quick'), quickGrid=q('.quick-grid'), summary=q('.summary-link'), summaryList=q('.summary-list'), truth=q('.truth-note');
return {
  viewport:{width:innerWidth,height:innerHeight},
  body_bg:cs(document.body).backgroundColor,
  sidebar:{width:rect(sidebar).width,bg:cs(sidebar).backgroundColor},
  brand:{...rel(brand,document.documentElement),font_size:num(cs(brand).fontSize),font_weight:num(cs(brand).fontWeight),color:cs(brand).color,mark_display:cs(q('.brand-mark')).display},
  nav:{item_width:rect(active).width,item_height:rect(active).height,item_radius:num(cs(active).borderRadius),gap:num(cs(q('.nav')).rowGap),active_bg:cs(active).backgroundColor,active_color:cs(active).color,active_weight:num(cs(active).fontWeight),inactive_color:cs(inactive).color,inactive_weight:num(cs(inactive).fontWeight)},
  topbar_display:cs(q('.top')).display,
  content:{...rel(content,document.documentElement)},
  home:{
    h1:{...rel(h1,content),font_size:num(cs(h1).fontSize),font_weight:num(cs(h1).fontWeight),color:cs(h1).color},
    lead:{...rel(lead,content),font_size:num(cs(lead).fontSize),font_weight:num(cs(lead).fontWeight),color:cs(lead).color},
    live:{...rel(live,content),bg:cs(live).backgroundColor,color:cs(live).color,font_size:num(cs(live).fontSize),font_weight:num(cs(live).fontWeight),radius:num(cs(live).borderRadius)},
    device:{...rel(device,content),bg:cs(device).backgroundColor,radius:num(cs(device).borderRadius)},
    quick:{...rel(quick,content),bg:cs(quick).backgroundColor,radius:num(cs(quick).borderRadius),gap:num(cs(quickGrid).columnGap)},
    summary:{...rel(summary,content),bg:cs(summary).backgroundColor,radius:num(cs(summary).borderRadius),gap:num(cs(summaryList).rowGap)},
    truth:{...rel(truth,content),font_size:num(cs(truth).fontSize),font_weight:num(cs(truth).fontWeight),color:cs(truth).color}
  }
};`)
}

func captureCompanionVisualContract(t *testing.T, wd *webDriverClient) map[string]any {
	t.Helper()
	return scriptMap(t, wd, `
const q=s=>document.querySelector(s), cs=e=>getComputedStyle(e), num=v=>parseFloat(v)||0, rect=e=>e.getBoundingClientRect();
const content=q('.content'), rel=e=>{const r=rect(e),p=rect(content);return {x:r.x-p.x,y:r.y-p.y,width:r.width,height:r.height}};
const h1=q('.head h1'), lead=q('.lead'), state=q('.state-card'), title=q('.section-title'), list=q('.control-list'), row=q('.control-row'), advanced=q('.advanced');
return {
  h1:{...rel(h1),font_size:num(cs(h1).fontSize),font_weight:num(cs(h1).fontWeight),color:cs(h1).color},
  lead:{...rel(lead),font_size:num(cs(lead).fontSize),font_weight:num(cs(lead).fontWeight),color:cs(lead).color},
  state:{...rel(state),bg:cs(state).backgroundColor,radius:num(cs(state).borderRadius)},
  section_title:{...rel(title),font_size:num(cs(title).fontSize),font_weight:num(cs(title).fontWeight),color:cs(title).color},
  settings:{...rel(list),gap:num(cs(list).rowGap)},
  row:{...rel(row),bg:cs(row).backgroundColor,radius:num(cs(row).borderRadius)},
  advanced:{...rel(advanced),bg:cs(advanced).backgroundColor,radius:num(cs(advanced).borderRadius)}
};`)
}

func captureMobileVisualContract(t *testing.T, wd *webDriverClient) map[string]any {
	t.Helper()
	return scriptMap(t, wd, `
const q=s=>document.querySelector(s), cs=e=>getComputedStyle(e), num=v=>parseFloat(v)||0, rect=e=>e.getBoundingClientRect();
const h1=q('.head h1'), device=q('.device-summary'), nav=q('.mobile-nav'), item=q('.mobile-nav button.active');
return {
  viewport:{width:innerWidth,height:innerHeight},
  body_bg:cs(document.body).backgroundColor,
  h1:{x:rect(h1).x,y:rect(h1).y,width:rect(h1).width,height:rect(h1).height,font_size:num(cs(h1).fontSize),font_weight:num(cs(h1).fontWeight),color:cs(h1).color},
  device:{x:rect(device).x,y:rect(device).y,width:rect(device).width,height:rect(device).height,bg:cs(device).backgroundColor,radius:num(cs(device).borderRadius)},
  nav:{x:rect(nav).x,y:rect(nav).y,width:rect(nav).width,height:rect(nav).height,item_height:rect(item).height,item_radius:num(cs(item).borderRadius),item_font_size:num(cs(item).fontSize),active_bg:cs(item).backgroundColor,active_color:cs(item).color}
};`)
}

func captureScreenshot(t *testing.T, wd *webDriverClient, path string) {
	t.Helper()
	value := wd.must(t, http.MethodGet, "/session/"+wd.sessionID+"/screenshot", nil)
	encoded, ok := value.(string)
	if !ok || encoded == "" {
		t.Fatalf("WebDriver screenshot returned %#v", value)
	}
	png, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode screenshot: %v", err)
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatalf("write screenshot %s: %v", path, err)
	}
}

func ownerHubVisualFixture() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/owner/dashboard", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, dashboardHTML)
	})
	responses := map[string]string{
		"/v1/owner/data/devices":      `{"devices":[{"device_id":"companion-v3","connection_status":"online"}]}`,
		"/v1/owner/data/device":       `{"has_device":true,"device_id":"companion-v3","connection_status":"online","desired":{"wake_model":"hey_bin","wake_threshold":0.72,"smart_vad_enabled":true,"ota_poll_interval_s":21600,"vad_threshold":950,"vad_silence_ms":700,"vad_min_speech_ms":180},"reported":{"wake_model":"hey_bin","wake_threshold":0.72,"smart_vad_enabled":true,"ota_poll_interval_s":21600,"vad_threshold":950,"vad_silence_ms":700,"vad_min_speech_ms":180},"desired_version":12,"reported_version":12,"settings_status":{"state":"applied"},"updated_at":"2026-08-21T12:00:00Z"}`,
		"/v1/owner/data/overview":     `{"month_total":125000,"monthly_budget":5000000,"budget_set":true,"expenses":[],"notes":[{"id":1,"content":"USB-C cable"}],"voice_memos":[],"reminders":[{"id":1,"title":"Call mom"}]}`,
		"/v1/owner/data/expenses":     `{"total_vnd":125000,"expenses":[{"id":1,"amount_vnd":125000,"category":"food","description":"Lunch","occurred_at":"2026-08-21T05:00:00Z"}]}`,
		"/v1/owner/data/savings-goal": `{"set":true,"goal":{"target_vnd":2000000,"description":"Monthly saving"}}`,
		"/v1/owner/data/notes":        `{"notes":[{"id":1,"content":"Buy a USB-C cable","created_at":"2026-08-21T05:00:00Z"}]}`,
		"/v1/owner/data/journal":      `{"journal":[{"id":1,"content":"Good progress today.","occurred_at":"2026-08-21T05:00:00Z"}]}`,
		"/v1/owner/data/reminders":    `{"reminders":[{"id":1,"title":"Call mom","fire_at":"2026-08-21T13:00:00Z","status":"scheduled"}],"timers":[]}`,
		"/v1/owner/data/voice-memos":  `{"voice_memos":[]}`,
		"/v1/owner/data/privacy":      `{"privacy":{"save_voice_audio":false,"voice_mail_policy":"ephemeral","long_term_memory_enabled":true,"conversation_retention_days":30,"voice_memo_retention_days":30,"memory_retention_days":90}}`,
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
