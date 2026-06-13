package main

import "testing"

// A trimmed but representative slice of a DuckDuckGo HTML results page: one
// result behind the /l/?uddg= redirect wrapper (with &amp;-encoded params and a
// <b> tag in the snippet), and one with a direct href.
const ddgFixture = `
<div class="result results_links">
  <div class="result__body">
    <h2 class="result__title">
      <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fminti%2Fdocs&amp;rut=abc123">MINTI Docs &amp; Guide</a>
    </h2>
    <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fminti%2Fdocs">The <b>MINTI</b> documentation covers the agent loop.</a>
  </div>
</div>
<div class="result results_links">
  <div class="result__body">
    <h2 class="result__title">
      <a rel="nofollow" class="result__a" href="https://golang.org/doc/">Go Documentation</a>
    </h2>
    <a class="result__snippet" href="https://golang.org/doc/">Official Go docs.</a>
  </div>
</div>
`

func TestParseDDGResults(t *testing.T) {
	hits := parseDDGResults(ddgFixture, 10)
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(hits), hits)
	}

	// First hit: redirect unwrapped, entities decoded, snippet de-tagged.
	if hits[0].URL != "https://example.com/minti/docs" {
		t.Errorf("hit[0].URL = %q, want unwrapped https://example.com/minti/docs", hits[0].URL)
	}
	if hits[0].Title != "MINTI Docs & Guide" {
		t.Errorf("hit[0].Title = %q, want %q", hits[0].Title, "MINTI Docs & Guide")
	}
	if hits[0].Snippet != "The MINTI documentation covers the agent loop." {
		t.Errorf("hit[0].Snippet = %q", hits[0].Snippet)
	}

	// Second hit: direct href passes through.
	if hits[1].URL != "https://golang.org/doc/" {
		t.Errorf("hit[1].URL = %q, want direct https://golang.org/doc/", hits[1].URL)
	}
}

func TestParseDDGResultsLimit(t *testing.T) {
	if hits := parseDDGResults(ddgFixture, 1); len(hits) != 1 {
		t.Errorf("limit=1 → %d hits, want 1", len(hits))
	}
}

func TestParseDDGResultsEmpty(t *testing.T) {
	if hits := parseDDGResults("<html><body>no results here</body></html>", 10); len(hits) != 0 {
		t.Errorf("expected 0 hits from a resultless page, got %d", len(hits))
	}
}

func TestUnwrapDDGURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Ffoo.com%2Fa%3Fx%3D1&rut=z", "https://foo.com/a?x=1"},
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Fbar.org&amp;rut=z", "https://bar.org"},
		{"https://direct.example/page", "https://direct.example/page"},
		{"//cdn.example/asset", "https://cdn.example/asset"},
		{"", ""},
		{"/relative/only", ""},
	}
	for _, c := range cases {
		if got := unwrapDDGURL(c.in); got != c.want {
			t.Errorf("unwrapDDGURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
