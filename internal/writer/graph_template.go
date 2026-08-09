package writer

import "html/template"

// pageTemplate renders the whole report as one self-contained document: no
// stylesheet, no script, no font, no image is fetched, so the file opens
// straight from disk.
//
// Colours are CSS custom properties rather than SVG fill attributes, which is
// what lets one document serve both colour schemes.
//
// Three data roles, all taken unchanged from the dataviz reference palette: one
// series blue (categorical slot 1), plus the documented gridline and baseline
// greys. The two charts are small multiples of the same measure on one scale, so
// they share the one accent — varying colour between them would falsely imply
// the measure differs, and each series is already named by its figcaption.
// Validated against both surfaces: every check passes, worst-pair CVD ΔE 21.6
// light / 19.2 dark.
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

  --series:   #2a78d6;
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

    --series:   #3987e5;
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

  --series:   #3987e5;
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
   the card instead of scrolling, and both charts stay time-aligned. */
svg { display: block; width: 100%; height: auto; }
text { fill: var(--text-muted); font-size: 10px; }
text.tick { text-anchor: end; font-variant-numeric: tabular-nums; }

.grid { stroke: var(--grid); stroke-width: 1; }
.zero { stroke: var(--baseline); stroke-width: 1; }

/* non-scaling-stroke keeps the line a hairline: the 952-unit viewBox is sized
   at 100% width, so a plain stroke-width thickens with the card. */
.area      { fill: var(--series); fill-opacity: 0.22; }
.area-line { fill: none; stroke: var(--series); stroke-width: 1.5;
             vector-effect: non-scaling-stroke; }

/* One transparent full-slot target per commit-bearing bucket; hover and
   keyboard focus surface the same tooltip. */
.hit { fill: transparent; }
.hit:hover { fill: var(--text-primary); fill-opacity: 0.05; }
.hit:focus {
  fill: var(--text-primary); fill-opacity: 0.05;
  stroke: var(--text-primary); stroke-width: 1; outline: none;
}

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
  <p class="figure-note">Total lines of code standing at the end of each {{.BucketNoun}} — the
  area steps where commits landed and holds flat across quiet stretches, so a history that only
  grows only rises. Both charts share one scale, so the height of one is directly comparable
  with the height of the other. These are counts taken from a cloc snapshot of each commit, not
  diff line counts: a commit that rewrites 100 lines in place leaves the total where it was.</p>
  {{range .Charts}}<figure>
    <figcaption>{{.Label}}</figcaption>
    <svg viewBox="0 0 {{$.Frame.Width}} {{$.Frame.Height}}" role="img" aria-label="{{.AriaLabel}}">
      {{range .YTicks}}<line class="{{.Class}}" x1="{{$.Frame.PlotLeft}}" x2="{{$.Frame.PlotRight}}" y1="{{.Y}}" y2="{{.Y}}"></line>
      <text class="tick" x="{{$.Frame.TickLabelX}}" y="{{.LabelY}}">{{.Text}}</text>
      {{end}}<path class="area" d="{{.Area.Fill}}"></path>
      <path class="area-line" d="{{.Area.Line}}"></path>
      {{range .XLabels}}<text x="{{.X}}" y="{{$.Frame.XLabelY}}">{{.Text}}</text>
      {{end}}{{range .Hits}}<rect class="hit" x="{{.X}}" y="{{$.Frame.PlotTop}}" width="{{.W}}" height="{{$.Frame.PlotHeight}}" tabindex="0"><title>{{.Title}}</title></rect>
      {{end}}
    </svg>
  </figure>
  {{end}}
</section>
{{end}}

<details>
  <summary>Table view — by {{.BucketNoun}}</summary>
  <table>
    <thead><tr>
      <th>{{.BucketColumn}}</th>
      <th class="num">Commits</th><th class="num">Product</th><th class="num">Test</th><th class="num">Total</th>
      <th class="num">Product Δ</th><th class="num">Test Δ</th><th class="num">Total Δ</th>
    </tr></thead>
    <tbody>
      {{range .BucketRows}}<tr>
        <td>{{.When}}</td>
        <td class="num">{{.Commits}}</td><td class="num">{{.Product}}</td>
        <td class="num">{{.Test}}</td><td class="num">{{.Total}}</td>
        <td class="num">{{.ProductDelta}}</td><td class="num">{{.TestDelta}}</td>
        <td class="num">{{.Delta}}</td>
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
