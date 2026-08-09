package writer

import "html/template"

// pageTemplate renders the whole report as one self-contained document: no
// stylesheet, no script, no font, no image is fetched, so the file opens
// straight from disk.
//
// Colours are CSS custom properties rather than SVG fill attributes, which is
// what lets one document serve both colour schemes.
//
// Four data roles, all taken unchanged from the dataviz reference palette: the
// diverging pair blue ↔ red for added ↔ removed (categorical slots 1 and 8),
// plus the documented gridline and baseline greys. Direction already encodes
// the sign, so colour here is redundant with position — the correct double
// encoding for a diverging scale. Validated as a two-slot palette against both
// surfaces: every check passes, worst-pair CVD ΔE 21.6 light / 19.2 dark.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root {
  color-scheme: light;
  --page:           #f9f9f7;
  --surface:        #fcfcfb;
  --text-primary:   #0b0b0b;
  --text-secondary: #52514e;
  --text-muted:     #898781;
  --border:         rgba(11, 11, 11, 0.10);

  --added:    #2a78d6;
  --removed:  #e34948;
  --grid:     #e1e0d9;
  --baseline: #c3c2b7;
}
@media (prefers-color-scheme: dark) {
  :root:where(:not([data-theme="light"])) {
    color-scheme: dark;
    --page:           #0d0d0d;
    --surface:        #1a1a19;
    --text-primary:   #ffffff;
    --text-secondary: #c3c2b7;
    --text-muted:     #898781;
    --border:         rgba(255, 255, 255, 0.10);

    --added:    #3987e5;
    --removed:  #e66767;
    --grid:     #2c2c2a;
    --baseline: #383835;
  }
}
:root[data-theme="dark"] {
  color-scheme: dark;
  --page:           #0d0d0d;
  --surface:        #1a1a19;
  --text-primary:   #ffffff;
  --text-secondary: #c3c2b7;
  --text-muted:     #898781;
  --border:         rgba(255, 255, 255, 0.10);

  --added:    #3987e5;
  --removed:  #e66767;
  --grid:     #2c2c2a;
  --baseline: #383835;
}

* { box-sizing: border-box; }
body {
  margin: 0;
  padding: 2rem 1.25rem 4rem;
  background: var(--page);
  color: var(--text-primary);
  font: 14px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
}
main { max-width: 60rem; margin: 0 auto; }

h1 { margin: 0; font-size: 1.35rem; font-weight: 600; letter-spacing: -0.01em; }
.subtitle { margin: 0.25rem 0 0; color: var(--text-secondary); }

.card {
  margin-top: 1.5rem;
  padding: 1.25rem;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
}

.tiles { display: flex; flex-wrap: wrap; gap: 1.75rem; }
.tile-label { color: var(--text-secondary); font-size: 0.8125rem; }
.tile-value { margin-top: 0.125rem; font-size: 1.5rem; font-weight: 600; letter-spacing: -0.02em; }
.tile-note { color: var(--text-muted); font-size: 0.75rem; }

.figure-note { margin: 0 0 1.25rem; color: var(--text-secondary); }
figure { margin: 0; }
figure + figure { margin-top: 1.5rem; }
figcaption { margin-bottom: 0.25rem; color: var(--text-secondary); font-weight: 600; }

/* The viewBox is fixed and the whole history fits it, so the chart scales to
   the card instead of scrolling, and both charts stay column-aligned. */
svg { display: block; width: 100%; height: auto; }
text { fill: var(--text-muted); font-size: 10px; }
text.tick { text-anchor: end; font-variant-numeric: tabular-nums; }

.grid { stroke: var(--grid); stroke-width: 1; }
.zero { stroke: var(--baseline); stroke-width: 1; }

.bar.up { fill: var(--added); }
.bar.down { fill: var(--removed); }

/* One transparent full-slot target per commit-bearing day; hover and keyboard
   focus surface the same tooltip. */
.hit { fill: transparent; }
.hit:hover { fill: var(--text-primary); fill-opacity: 0.05; }
.hit:focus {
  fill: var(--text-primary); fill-opacity: 0.05;
  stroke: var(--text-primary); stroke-width: 1; outline: none;
}

.legend { display: flex; align-items: center; gap: 1rem; margin-top: 1rem; flex-wrap: wrap; }
.key { display: inline-flex; align-items: center; gap: 0.375rem; color: var(--text-secondary); font-size: 0.8125rem; }
.swatch { width: 15px; height: 15px; border-radius: 2px; border: 1px solid var(--border); }
.swatch.added { background: var(--added); }
.swatch.removed { background: var(--removed); }

details { margin-top: 1.5rem; }
summary { cursor: pointer; color: var(--text-secondary); }
table { width: 100%; margin-top: 0.875rem; border-collapse: collapse; font-size: 0.8125rem; }
th, td { padding: 0.375rem 0.5rem; text-align: left; border-bottom: 1px solid var(--border); }
th { color: var(--text-secondary); font-weight: 600; }
td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
td.sha { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--text-secondary); }
.empty { color: var(--text-secondary); }
</style>
</head>
<body>
<main>
<header>
  <h1>{{.Title}}</h1>
  {{if .Subtitle}}<p class="subtitle">{{.Subtitle}}</p>{{end}}
</header>
{{if .Empty}}
<div class="card"><p class="empty">No commits to chart.</p></div>
{{else}}
<section class="card tiles">
  {{range .Tiles}}<div>
    <div class="tile-label">{{.Label}}</div>
    <div class="tile-value">{{.Value}}</div>
    <div class="tile-note">{{.Note}}</div>
  </div>{{end}}
</section>

<section class="card">
  <p class="figure-note">Net lines of code each day — one column per day, up where the tree
  grew and down where it shrank. Both charts share one scale, so a column in one is directly
  comparable with a column in the other. These are net counts differenced from a cloc snapshot
  of each commit, not diff line counts: a commit that rewrites 100 lines in place nets to zero
  and draws no column.</p>
  {{range .Charts}}<figure>
    <figcaption>{{.Label}}</figcaption>
    <svg viewBox="0 0 {{$.Frame.Width}} {{$.Frame.Height}}" role="img" aria-label="{{.AriaLabel}}">
      {{range .YTicks}}<line class="{{.Class}}" x1="{{$.Frame.PlotLeft}}" x2="{{$.Frame.PlotRight}}" y1="{{.Y}}" y2="{{.Y}}"></line>
      <text class="tick" x="{{$.Frame.TickLabelX}}" y="{{.LabelY}}">{{.Text}}</text>
      {{end}}{{range .Bars}}<rect class="{{.Class}}" x="{{.X}}" y="{{.Y}}" width="{{.W}}" height="{{.H}}"></rect>
      {{end}}{{range .Months}}<text x="{{.X}}" y="{{$.Frame.MonthLabelY}}">{{.Text}}</text>
      {{end}}{{range .Hits}}<rect class="hit" x="{{.X}}" y="{{$.Frame.PlotTop}}" width="{{.W}}" height="{{$.Frame.PlotHeight}}" tabindex="0"><title>{{.Title}}</title></rect>
      {{end}}
    </svg>
  </figure>
  {{end}}<div class="legend">
    {{range .Key}}<span class="key"><span class="swatch {{.Class}}"></span>{{.Label}}</span>
    {{end}}
  </div>
</section>
{{end}}

<details>
  <summary>Table view — by day</summary>
  <table>
    <thead><tr>
      <th>Date</th>
      <th class="num">Commits</th><th class="num">Product Δ</th><th class="num">Test Δ</th><th class="num">Total Δ</th>
    </tr></thead>
    <tbody>
      {{range .DayRows}}<tr>
        <td>{{.Date}}</td>
        <td class="num">{{.Commits}}</td><td class="num">{{.Product}}</td>
        <td class="num">{{.Test}}</td><td class="num">{{.Total}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
</details>

<details>
  <summary>Table view — every commit</summary>
  <table>
    <thead><tr>
      <th>Date</th><th>Commit</th><th>Subject</th>
      <th class="num">Product</th><th class="num">Test</th><th class="num">Total</th><th class="num">Δ</th>
    </tr></thead>
    <tbody>
      {{range .Rows}}<tr>
        <td>{{.Date}}</td><td class="sha">{{.Short}}</td><td>{{.Subject}}</td>
        <td class="num">{{.Product}}</td><td class="num">{{.Test}}</td>
        <td class="num">{{.Total}}</td><td class="num">{{.Delta}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
</details>
</main>
</body>
</html>
`))
