package main

import (
	"os"
	"strings"
	"testing"
)

func TestColorsFromTokens(t *testing.T) {
	css, err := os.ReadFile("../../static/ui.css") // the test runs in its own directory
	if err != nil {
		t.Fatal(err)
	}
	light, dark, err := colors(string(css))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tokens {
		if !strings.Contains(light, "--"+name+":") {
			t.Errorf("light lacks --%s", name)
		}
	}
	if !strings.Contains(dark, "--bg:") || strings.Contains(dark, "--font:") {
		t.Errorf("dark = %q", dark)
	}
	if _, _, err := colors(":root{--bg:#fff;}"); err == nil {
		t.Error("css without a dark scheme accepted")
	}
}
