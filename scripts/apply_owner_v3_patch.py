#!/usr/bin/env python3
"""One-shot source migration for #281. Removed before merge."""
from pathlib import Path

path = Path("backend/internal/ownerweb/dashboard.html")
text = path.read_text(encoding="utf-8")

replacements = [
    (
        ':root{--bg:#0e0f14;--nav:#0b0c11;--surface:#15171e;--surface2:#111319;--surface3:#1c1f28;--line:rgba(255,255,255,.075);--text:#f7f7fb;--muted:#9a9fac;--dim:#6f7480;--accent:#7569ef;--accent2:#9d95ff;--accent-soft:rgba(117,105,239,.13);--ok:#46c99a;--warn:#e9b665;--bad:#ff7a82;--radius:18px;',
        ':root{--bg:#0f1115;--nav:#0b0d11;--surface:#16191f;--surface2:#13161b;--surface3:#1d222b;--line:rgba(255,255,255,.075);--text:#f7f8fb;--muted:#9fa6b2;--dim:#707887;--accent:#7d73f4;--accent2:#978fff;--accent-soft:#1a172e;--ok:#38c992;--warn:#efb95f;--bad:#ff737d;--radius:16px;',
    ),
    ('.app{height:100vh;display:grid;grid-template-columns:200px minmax(0,1fr)}', '.app{height:100vh;display:grid;grid-template-columns:190px minmax(0,1fr)}'),
    ('.sidebar{background:var(--nav);border-right:1px solid var(--line);padding:22px 16px 18px;display:flex;flex-direction:column}', '.sidebar{background:var(--nav);border-right:1px solid var(--line);padding:22px 18px 18px;display:flex;flex-direction:column}'),
    ('.brand{display:flex;align-items:center;gap:10px;font-weight:700;font-size:15px;padding:0 8px 28px;letter-spacing:-.01em}', '.brand{height:52px;display:flex;align-items:flex-start;gap:0;font-weight:600;font-size:16px;line-height:19px;padding:0 2px;letter-spacing:0}'),
    ('.brand-mark{width:28px;height:28px;border-radius:9px;display:grid;place-items:center;background:linear-gradient(145deg,var(--accent2),var(--accent));box-shadow:0 8px 26px rgba(117,105,239,.24);font-size:11px;color:white}', '.brand-mark{display:none}'),
    ('.nav{display:grid;gap:5px}', '.nav{display:grid;gap:6px}'),
    ('.nav button{min-height:44px;border:0;background:transparent;color:var(--muted);padding:11px 12px;border-radius:12px;text-align:left;font-size:12px;transition:background .16s ease,color .16s ease}', '.nav button{height:42px;min-height:42px;border:0;background:transparent;color:var(--muted);padding:0 12px;border-radius:11px;text-align:left;font-size:12px;font-weight:400;transition:background .16s ease,color .16s ease}'),
    ('.nav button.active{background:var(--accent-soft);color:#eeeaff;font-weight:650}', '.nav button.active{background:var(--accent-soft);color:#e8e3ff;font-weight:600}'),
    ('.top{position:sticky;top:0;z-index:20;height:64px;padding:0 32px;background:rgba(14,15,20,.88);backdrop-filter:blur(18px);display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid var(--line)}', '.top{display:none}'),
    ('.content{max-width:980px;margin:0 auto;padding:38px 32px 104px}', '.content{width:100%;max-width:930px;margin:0;padding:28px 30px 104px}'),
    ('.head{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;margin-bottom:28px}', '.head{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;margin-bottom:29px}'),
    ('.head h1{font-size:32px;line-height:1.08;letter-spacing:-.045em;margin:0;font-weight:680}', '.head h1{font-size:29px;line-height:35px;letter-spacing:0;margin:0;font-weight:600}'),
    ('.lead{color:var(--muted);font-size:12px;line-height:1.5;margin-top:9px}', '.lead{color:var(--muted);font-size:11px;line-height:13px;margin-top:7px}'),
    ('.live{font-size:10px;font-weight:700;letter-spacing:.04em;color:var(--ok);background:rgba(70,201,154,.1);border:1px solid rgba(70,201,154,.12);padding:6px 9px;border-radius:999px}', '.live{height:22px;min-width:40px;display:grid;place-items:center;margin-top:2px;margin-right:54px;font-size:10px;font-weight:600;letter-spacing:0;color:var(--ok);background:rgba(56,201,146,.12);border:0;padding:0 9px;border-radius:999px}'),
    ('.section{margin-top:30px}', '.section{margin-top:28px}'),
    ('.section-title{font-size:14px;font-weight:650;margin:0 0 12px;color:#e9e9ef}', '.section-title{font-size:15px;font-weight:600;line-height:18px;margin:0 0 12px;color:var(--text)}'),
    ('.device-summary{padding:20px;border:1px solid rgba(117,105,239,.17);border-radius:20px;background:linear-gradient(145deg,rgba(117,105,239,.08),rgba(255,255,255,.012))}', '.device-summary{min-height:116px;padding:20px 18px;border:0;border-radius:20px;background:var(--surface2)}'),
    ('.device-summary h2{font-size:17px;margin:0}', '.device-summary h2{font-size:17px;font-weight:600;margin:0}'),
    ('.quick-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:10px}', '.quick-grid{width:858px;max-width:100%;display:grid;grid-template-columns:repeat(3,1fr);gap:12px}'),
    ('.quick{min-height:88px;text-align:left;padding:16px;border:1px solid var(--line);background:var(--surface);border-radius:15px;transition:transform .16s ease,background .16s ease}', '.quick{height:78px;min-height:78px;text-align:left;padding:14px;border:0;background:var(--surface);border-radius:14px;transition:transform .16s ease,background .16s ease}'),
    ('.summary-list{display:grid;gap:8px}', '.summary-list{width:826px;max-width:100%;display:grid;gap:8px}'),
    ('.summary-link{min-height:68px;width:100%;display:grid;grid-template-columns:1fr auto;align-items:center;text-align:left;border:1px solid transparent;background:var(--surface2);border-radius:13px;padding:13px 15px}', '.summary-link{height:64px;min-height:64px;width:100%;display:grid;grid-template-columns:1fr auto;align-items:center;text-align:left;border:0;background:var(--surface2);border-radius:12px;padding:12px 14px}'),
    ('.summary-link b{font-size:13px}', '.summary-link b{font-size:13px;font-weight:500}'),
    ('.state-card{padding:18px;background:var(--surface);border:1px solid var(--line);border-radius:16px}', '.state-card{min-height:100px;padding:18px 16px;background:var(--surface);border:0;border-radius:16px}'),
    ('.control-list{display:grid;gap:8px}', '.control-list{width:826px;max-width:100%;display:grid;gap:8px}'),
    ('.control-row{display:grid;grid-template-columns:minmax(0,1fr) minmax(190px,310px);gap:18px;align-items:center;min-height:68px;padding:11px 14px;background:var(--surface2);border:1px solid transparent;border-radius:13px}', '.control-row{display:grid;grid-template-columns:minmax(0,1fr) minmax(190px,310px);gap:18px;align-items:center;height:64px;min-height:64px;padding:10px 14px;background:var(--surface2);border:0;border-radius:12px}'),
    ('.row-title{font-size:13px;font-weight:600}', '.row-title{font-size:13px;font-weight:500}'),
    ('.advanced{margin-top:14px;border:1px solid var(--line);border-radius:15px;background:var(--surface);padding:13px}', '.advanced{min-height:94px;margin-top:0;border:0;border-radius:14px;background:var(--surface);padding:13px 14px}'),
    ('.advanced summary{min-height:44px;display:flex;align-items:center;cursor:pointer;color:var(--accent2);font-size:12px;font-weight:600}', '.advanced summary{min-height:30px;display:flex;align-items:center;cursor:pointer;color:var(--text);font-size:13px;font-weight:600}'),
    ('.sheet{border:0;padding:0;margin:0 0 0 auto;width:min(620px,calc(100vw - 200px));', '.sheet{border:0;padding:0;margin:0 0 0 auto;width:min(620px,calc(100vw - 190px));'),
    ('.head h1{font-size:29px}', '.head h1{font-size:21px;line-height:25px}'),
    ('.mobile-nav button{min-height:44px;border:0;background:transparent;color:var(--muted);border-radius:9px;font-size:10px}', '.mobile-nav button{height:42px;min-height:42px;border:0;background:transparent;color:var(--muted);border-radius:9px;font-size:8px}'),
    ('function pageHead(title,lead){return `<div class="head"><div><h1>${esc(title)}</h1><div class="lead">${esc(lead)}</div></div><span class="live">LIVE</span></div>`}', 'function pageHead(title,_lead){return `<div class="head"><div><h1>${esc(title)}</h1><div class="lead">Current product surface</div></div><span class="live">LIVE</span></div>`}'),
]

for old, new in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"expected exactly one source match, got {count}: {old[:100]}")
    text = text.replace(old, new, 1)

path.write_text(text, encoding="utf-8")
print(f"patched {path} with {len(replacements)} verified replacements")
