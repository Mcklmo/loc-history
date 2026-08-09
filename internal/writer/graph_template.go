package writer

import "html/template"

// pageTemplate renders the whole report as one self-contained document: no
// stylesheet, no script, no font, no image is fetched, so the file opens
// straight from disk.
//
// Colours are CSS custom properties rather than SVG fill attributes, which is
// what lets one document serve both colour schemes.
//
// The diverging ramp comes from the dataviz reference palette. The blue arm is
// its documented sequential blue (steps 150/300/450/600 in light, 600/450/400/250
// in dark, so "near zero" always recedes toward the surface). The reference
// palette documents no red ramp, so the red arm is computed rather than
// eyeballed: each step takes its blue counterpart's OKLCH lightness at the
// documented red's hue, with chroma scaled by the ratio between the two
// documented poles. Verified: |L − L_midpoint| rises monotonically along each
// arm in both modes, four steps per arm, neutral gray at the midpoint.
//
// The two innermost steps of each arm fall below 3:1 against the surface. That
// is deliberate for a sequential/diverging encoding — the near-zero end is
// allowed to recede — and it triggers the relief rule, which the table view and
// the per-cell titles satisfy: no value is reachable only by hovering.
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

  --cell-none: #e1e0d9;
  --cell-zero: #f0efec;
  --pos-1: #b7d3f6;
  --pos-2: #6da7ec;
  --pos-3: #2a78d6;
  --pos-4: #184f95;
  --neg-1: #fac0ba;
  --neg-2: #ee7e77;
  --neg-3: #d2383a;
  --neg-4: #911e22;
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

    --cell-none: #2c2c2a;
    --cell-zero: #383835;
    --pos-1: #184f95;
    --pos-2: #2a78d6;
    --pos-3: #3987e5;
    --pos-4: #86b6ef;
    --neg-1: #911e22;
    --neg-2: #d2383a;
    --neg-3: #e24a48;
    --neg-4: #f2958e;
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

  --cell-none: #2c2c2a;
  --cell-zero: #383835;
  --pos-1: #184f95;
  --pos-2: #2a78d6;
  --pos-3: #3987e5;
  --pos-4: #86b6ef;
  --neg-1: #911e22;
  --neg-2: #d2383a;
  --neg-3: #e24a48;
  --neg-4: #f2958e;
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

figure { margin: 0; }
figcaption { margin-bottom: 0.875rem; color: var(--text-secondary); }

/* Wide histories scroll inside the card; the page itself never does. */
.scroll { overflow-x: auto; padding-bottom: 0.25rem; }
svg { display: block; }
text { fill: var(--text-muted); font-size: 10px; }

.cell { rx: 2; ry: 2; stroke: var(--border); stroke-width: 0.5; }
.cell.none { fill: var(--cell-none); }
.cell.zero { fill: var(--cell-zero); }
.cell.pos1 { fill: var(--pos-1); }
.cell.pos2 { fill: var(--pos-2); }
.cell.pos3 { fill: var(--pos-3); }
.cell.pos4 { fill: var(--pos-4); }
.cell.neg1 { fill: var(--neg-1); }
.cell.neg2 { fill: var(--neg-2); }
.cell.neg3 { fill: var(--neg-3); }
.cell.neg4 { fill: var(--neg-4); }
/* The hovered mark responds, and the hit target is the whole cell. */
.cell:hover, .cell:focus { stroke: var(--text-primary); stroke-width: 1.5; outline: none; }

.legend { display: flex; align-items: center; gap: 0.5rem; margin-top: 1rem; flex-wrap: wrap; }
.legend-end { color: var(--text-secondary); font-size: 0.8125rem; white-space: nowrap; }
.swatches { display: flex; gap: 2px; }
.swatch { width: 15px; height: 15px; border-radius: 2px; border: 1px solid var(--border); }
.swatch.zero { background: var(--cell-zero); }
.swatch.pos1 { background: var(--pos-1); }
.swatch.pos2 { background: var(--pos-2); }
.swatch.pos3 { background: var(--pos-3); }
.swatch.pos4 { background: var(--pos-4); }
.swatch.neg1 { background: var(--neg-1); }
.swatch.neg2 { background: var(--neg-2); }
.swatch.neg3 { background: var(--neg-3); }
.swatch.neg4 { background: var(--neg-4); }
.legend-note { width: 100%; margin: 0; color: var(--text-muted); font-size: 0.75rem; }

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
<figure>
  <figcaption>Net lines of code added or removed each day. Deletions are shown as deletions, not as zero.</figcaption>
  <div class="scroll">
    <svg width="{{.Width}}" height="{{.Height}}" viewBox="0 0 {{.Width}} {{.Height}}" role="img"
         aria-label="Calendar heat map of daily net change in lines of code. The same values are listed in the table below.">
      {{range .Months}}<text x="{{.X}}" y="12">{{.Text}}</text>
      {{end}}{{range .Days}}<text x="0" y="{{.Y}}">{{.Text}}</text>
      {{end}}{{range .Cells}}<rect class="{{.Class}}" x="{{.X}}" y="{{.Y}}" width="` +
	itoa(cellSize) + `" height="` + itoa(cellSize) + `" tabindex="0"><title>{{.Title}}</title></rect>
      {{end}}
    </svg>
  </div>
  <div class="legend">
    <span class="legend-end">{{.Legend.Low}}</span>
    <span class="swatches">{{range .Legend.Swatches}}<span class="swatch {{.Class}}"{{if .Label}} title="{{.Label}}"{{end}}></span>{{end}}</span>
    <span class="legend-end">{{.Legend.High}}</span>
    <p class="legend-note">{{.Legend.Note}}</p>
  </div>
</figure>
</section>
{{end}}

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
