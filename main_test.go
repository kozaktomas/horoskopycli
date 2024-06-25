package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/antchfx/htmlquery"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/html"
)

func TestParsePrediction(t *testing.T) {
	containTable := []string{
		"Své kolegy můžete něčemu novému naučit. Je možné, že vás o to požádá někdo z nadřízených, nebo přímo vedení firmy.",
		"Ve vašem životě mohou nyní hrát velkou roli vaši přátelé",
		"nebo přímo vedení firmy.",
		"Aktivity vhodné pro dnešní den",
	}

	d, err := htmlquery.LoadDoc("test/example_response.html")
	assert.Nil(t, err)
	pred := parsePrediction(d)

	for _, s := range containTable {
		assert.True(t, strings.Contains(pred, s), fmt.Sprintf("Prediction should contain %q", s))
	}
}

func TestNodeHasClass(t *testing.T) {
	cases := map[string]struct {
		classAttr     string
		expectedClass string
		contains      bool
	}{
		"only class":        {classAttr: "lev", expectedClass: "lev", contains: true},
		"multiple classes":  {classAttr: "kral lev zvirat", expectedClass: "lev", contains: true},
		"does not contains": {classAttr: "kral lev zvirat", expectedClass: "pav", contains: false},
		"empty class":       {classAttr: "", expectedClass: "lev", contains: false},
		"all empty":         {classAttr: "", expectedClass: "", contains: false},
	}

	for msg, c := range cases {
		n := &html.Node{
			Attr: []html.Attribute{
				{
					Namespace: "",
					Key:       "class",
					Val:       c.classAttr,
				},
				{
					Namespace: "",
					Key:       "data-test",
					Val:       "kral lev zvirat", // testing attr
				},
			},
		}

		assert.Equal(t, c.contains, nodeHasClass(n, c.expectedClass), msg)
	}
}

func TestSanitizeSign(t *testing.T) {
	cases := map[string]struct {
		input    string
		expected string
	}{
		"valid sign":           {input: "lev", expected: "lev"},
		"remove special chars": {input: "šťír", expected: "stir"},
		"to lower":             {input: "Býk", expected: "byk"},
	}

	for msg, c := range cases {
		assert.Equal(t, c.expected, sanitizeSign(c.input), msg)
	}
}

func TestIsSignValid(t *testing.T) {
	assert.True(t, isSignValid("byk"))
	assert.False(t, isSignValid("dymovnica"))
}
