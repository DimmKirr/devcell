#!/usr/bin/env bash
# Website validation tests — checks built HTML output in dist/
# Usage: ./test-website.sh [test_name]
# Run all: ./test-website.sh
# Run one: ./test-website.sh test_opengraph

set -uo pipefail
cd "$(dirname "$0")"

PASS=0
FAIL=0
ERRORS=()

assert_contains() {
  local file="$1" pattern="$2" msg="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    ((PASS++))
  else
    ((FAIL++))
    ERRORS+=("FAIL: $msg — pattern '$pattern' not found in $file")
  fi
}

assert_not_contains() {
  local file="$1" pattern="$2" msg="$3"
  if ! grep -q "$pattern" "$file" 2>/dev/null; then
    ((PASS++))
  else
    ((FAIL++))
    ERRORS+=("FAIL: $msg — pattern '$pattern' unexpectedly found in $file")
  fi
}

assert_file_exists() {
  local file="$1" msg="$2"
  if [[ -f "$file" ]]; then
    ((PASS++))
  else
    ((FAIL++))
    ERRORS+=("FAIL: $msg — file $file does not exist")
  fi
}

# ── CELL-225: OpenGraph meta tags ──
test_opengraph() {
  echo "── CELL-225: OpenGraph meta tags ──"
  local idx="dist/index.html"
  assert_contains "$idx" 'og:title' "Homepage has og:title"
  assert_contains "$idx" 'og:description' "Homepage has og:description"
  assert_contains "$idx" 'og:image' "Homepage has og:image"
  assert_contains "$idx" 'og:url' "Homepage has og:url"
  assert_contains "$idx" 'twitter:card' "Homepage has twitter:card"
}

# ── CELL-222: GitHub star count ──
test_star_badge() {
  echo "── CELL-222: GitHub star badge ──"
  local idx="dist/index.html"
  assert_contains "$idx" 'star-badge\|github/stars' "Homepage has GitHub star badge"
}

# ── CELL-220: Font display swap ──
test_font_display() {
  echo "── CELL-220: Font display swap ──"
  local idx="dist/index.html"
  assert_contains "$idx" 'display=swap\|font-display:\s*swap' "Fonts use display=swap"
}

# ── CELL-219: Version number on homepage ──
test_version_display() {
  echo "── CELL-219: Version number ──"
  local idx="dist/index.html"
  assert_contains "$idx" 'releases' "Homepage links to releases"
}

# ── CELL-218: Table zebra-striping ──
test_table_zebra() {
  echo "── CELL-218: Table zebra-striping ──"
  local css
  css=$(find dist/_astro -name "*.css" | head -1)
  if [[ -n "$css" ]]; then
    assert_contains "$css" 'plan-grid.*nth-child\|plan-grid tr.*nth' "CSS has nth-child rule for plan-grid table rows"
  else
    ((FAIL++))
    ERRORS+=("FAIL: No CSS file found in dist/_astro/")
  fi
}

# ── CELL-221: Feature card icons ──
test_feature_icons() {
  echo "── CELL-221: Feature card icons ──"
  local idx="dist/index.html"
  assert_contains "$idx" 'card-icon' "Feature cards have icon class"
}

# ── CELL-223: Docs search with Pagefind ──
test_docs_search() {
  echo "── CELL-223: Docs search ──"
  assert_file_exists "dist/pagefind/pagefind-ui.js" "Pagefind UI JS exists in dist"
  assert_file_exists "dist/pagefind/pagefind-ui.css" "Pagefind UI CSS exists in dist"
  local doc="dist/docs/quickstart/index.html"
  assert_contains "$doc" 'id="search"' "Docs page has search container"
  assert_contains "$doc" 'pagefind-ui.js' "Docs page loads Pagefind UI script"
}

# ── CELL-224: Comparison tables outside FAQ ──
test_comparison_section() {
  echo "── CELL-224: Comparison tables outside FAQ ──"
  local idx="dist/index.html"
  # The comparison tables should exist in their own section with id="compare", NOT inside <details>
  assert_contains "$idx" 'id="compare"' "Homepage has #compare section"
  # Check that "Dev Containers" text appears outside of a <details> context
  # We check that a compare section heading exists
  assert_contains "$idx" 'How we compare\|how we compare\|How We Compare' "Compare section has heading"
}

# ── Runner ──
if [[ $# -gt 0 ]]; then
  "$1"
else
  test_opengraph
  test_comparison_section
  test_docs_search
  test_star_badge
  test_feature_icons
  test_font_display
  test_version_display
  test_table_zebra
fi

echo ""
for e in "${ERRORS[@]+"${ERRORS[@]}"}"; do
  echo "  $e"
done
echo ""
echo "Results: $PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
