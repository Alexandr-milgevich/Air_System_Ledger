package httpserver

import "net/http"

// appCSS is presentation-only. It does not depend on user data, permissions or state.
const appCSS = `
:root{color-scheme:light;--ink:#17253b;--muted:#64748b;--surface:#fff;--canvas:#f4f6fa;--edge:#dbe3ed;--brand:#1762a6;--brand-dark:#114c82;--focus:#f1a43b;--radius:13px}
*{box-sizing:border-box}body{background:var(--canvas);color:var(--ink);font:15px/1.55 system-ui,-apple-system,"Segoe UI",Arial,sans-serif;margin:0;min-width:320px}
header{background:#172a42;color:#fff;padding:13px max(20px,calc((100vw - 1360px)/2));display:flex;align-items:center;gap:20px;flex-wrap:wrap;box-shadow:0 1px 9px #12233a1c}
header b,header strong{font-size:17px;letter-spacing:-.02em;white-space:normal}header nav{display:flex;gap:5px;flex-wrap:wrap;margin:0}header nav a,header>a{display:inline-block;padding:8px 11px;border-radius:8px;text-decoration:none;color:#e9f0f9;margin:0}header nav a:hover,header>a:hover{background:#ffffff1a;color:white}header form.inline{margin-left:auto}
main{width:min(100%,1440px);max-width:1440px;margin:24px auto 64px;padding:0 22px}
main>h1,h1{font-size:clamp(24px,2.5vw,33px);line-height:1.24;letter-spacing:-.035em;margin:0 0 18px}h2{font-size:19px;letter-spacing:-.02em;line-height:1.35;margin:0 0 14px}h3{font-size:16px}
.panel,section,.card{background:var(--surface);border:1px solid var(--edge);border-radius:var(--radius);padding:23px;margin:16px 0;box-shadow:0 3px 12px #10274409}
.panel:first-child,section:first-child{margin-top:0}.grid{gap:16px}
a{color:var(--brand);text-underline-offset:3px}a:hover{color:var(--brand-dark)}p{margin:10px 0 14px}small,.muted{color:var(--muted)}
input:not([type=hidden]),select,textarea{border:1px solid #b9c7d8;background:#fff;color:var(--ink);border-radius:9px;padding:10px 12px;min-height:41px;max-width:100%;font:inherit}
input[type=checkbox],input[type=radio]{width:auto;min-height:0}input[type=file]{height:auto}label{font-weight:600;font-size:13px;margin:13px 0 5px}
button,.button{background:var(--brand);border:1px solid var(--brand);color:#fff;border-radius:9px;min-height:40px;padding:9px 16px;font:600 14px/1.3 inherit;cursor:pointer;white-space:normal}button:hover{background:var(--brand-dark);border-color:var(--brand-dark)}button:disabled{opacity:.6;cursor:not-allowed}
:focus-visible{outline:3px solid var(--focus);outline-offset:2px}form{max-width:100%}
table{width:100%;border-collapse:collapse;font-size:14px}thead th,table tr:first-child th{background:#f1f5fa;color:#50617a;text-align:left;font-size:12px;font-weight:700;letter-spacing:.015em}td,th{padding:11px 13px;border-bottom:1px solid #e6ebf2;vertical-align:top}tbody tr:hover{background:#f8fafd}td.num,th.num{font-variant-numeric:tabular-nums}table{display:block;overflow-x:auto;max-width:100%}table tbody,table thead{width:100%}
.search-form{margin:4px 0 18px}.search-controls{display:flex;align-items:center;flex-wrap:wrap;gap:10px}.search-controls input{width:min(460px,100%);flex:1 1 210px}.search-controls button{margin:0;display:inline-block}.search-controls a{font-size:13px}.table-scroll{overflow:auto;max-width:100%}.table-scroll table{display:table;min-width:520px}
.stat{background:#f8fafc;border-color:var(--edge);border-radius:11px}.stat b{font-variant-numeric:tabular-nums;letter-spacing:-.02em}.error,.warn{background:#fff7ed;border-left:3px solid #d97706;padding:11px 14px;border-radius:7px;color:#92400e}
.dashboard-cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px;margin:20px 0}.dashboard-cards>div{background:#f4f8fd;border:1px solid #dbe6f3;border-radius:12px;padding:15px}.dashboard-cards small{display:block;font-size:12px}.dashboard-cards strong{display:block;font-size:23px;font-variant-numeric:tabular-nums;margin-top:5px}.ledger-filters{display:flex;align-items:end;gap:12px;flex-wrap:wrap}.ledger-filters label{flex:1 1 230px;margin:0}.ledger-filters label input,.ledger-filters label select{display:block;width:100%;margin-top:6px}.ledger-filters button{margin:0}.ledger-filters a{margin-bottom:8px}
@media(max-width:760px){header{padding:13px 16px;gap:10px}header nav{width:100%;overflow-x:auto}header nav a{font-size:13px;padding:7px 9px}header form.inline{margin-left:0}main{padding:0 12px;margin:18px auto 45px}.panel,section,.card{padding:15px;border-radius:11px}.grid{grid-template-columns:1fr!important;gap:12px}h1{font-size:25px}table{font-size:13px}td,th{padding:9px 10px}.search-controls button{margin:0}}
@media(prefers-reduced-motion:no-preference){button,a,input,select{transition:background-color .12s ease,border-color .12s ease}}
@media print{body{background:white;color:black}header,form,button{display:none!important}main{margin:0;padding:0;max-width:none}.panel,section,.card{box-shadow:none;break-inside:avoid}}
`

func appCSSHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(appCSS))
}
