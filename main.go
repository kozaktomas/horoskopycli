package main

import (
	"fmt"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"unicode"
)

const baseUrl = "https://www.horoskopy.cz"

var signs = []string{
	"beran",
	"lev",
	"strelec",
	"byk",
	"panna",
	"kozoroh",
	"blizenci",
	"vahy",
	"vodnar",
	"rak",
	"stir",
	"ryby",
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Please provide your sign")
		fmt.Println("Signs: " + strings.Join(signs, ", "))
		os.Exit(0)
	}

	sign := sanitizeSign(os.Args[1])
	if !isSignValid(sign) {
		fmt.Println("Please provide valid sign")
		fmt.Println("Valid signs: " + strings.Join(signs, ", "))
		os.Exit(1)
	}

	fmt.Print(loadPrediction(sign))
}

func loadPrediction(sign string) string {
	url := fmt.Sprintf("%s/%s", baseUrl, sign)
	res, err := http.DefaultClient.Get(url)
	if err != nil {
		log.Fatalf("Could not get data from the server: %s", err)
	}
	doc, err := html.Parse(res.Body)
	if err != nil {
		log.Fatalf("Could not parse server response: %s", err)
	}

	return parsePrediction(doc)
}

func parsePrediction(document *html.Node) string {
	contents, err := htmlquery.QueryAll(document, "//*[@id=\"content-detail\"]")
	if err != nil {
		log.Fatalf("Invalid XPath expression: %s", err)
	}
	if len(contents) != 1 {
		log.Fatalf("Could not find contant element")
	}
	content := contents[0]
	read := false
	el := content.FirstChild
	sb := strings.Builder{}
	for el != nil {
		if el.Data == "h1" {
			sb.WriteString(el.FirstChild.Data + "\n")
			sb.WriteString(strings.Repeat("=", len(el.FirstChild.Data)) + "\n\n")
		}

		if el.Data == "h2" {
			read = true
		}

		if read && el.Data == "div" && nodeHasClass(el, "brown") {
			sb.WriteString("=> " + sanitizeString(el.FirstChild.Data) + "\n")
		}

		if read && el.Data == "p" {
			sb.WriteString(sanitizeString(el.FirstChild.Data) + "\n\n")
		}

		if el.Data == "div" && nodeHasClass(el, "cleaner") {
			read = false
		}

		el = el.NextSibling
	}

	s := sb.String()
	return s
}

var spaceReg = regexp.MustCompile(`\s+`)

func sanitizeString(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = spaceReg.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	return s
}

func nodeHasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if strings.ToLower(attr.Key) == "class" {
			if attr.Val == "" {
				return false
			}
			classes := strings.Split(attr.Val, " ")
			for _, c := range classes {
				if c == class {
					return true
				}
			}
		}
	}

	return false
}

// https://stackoverflow.com/questions/26722450/remove-diacritics-using-go
type mns struct {
}

func (a mns) Contains(r rune) bool {
	return unicode.Is(unicode.Mn, r) // Mn: nonspacing marks
}

func sanitizeSign(sign string) string {
	sign = strings.ToLower(sign)
	var x mns
	t := transform.Chain(norm.NFD, runes.Remove(x), norm.NFC)
	sign, _, _ = transform.String(t, sign)
	return sign
}

func isSignValid(sign string) bool {
	for _, s := range signs {
		if s == sign {
			return true
		}
	}

	return false
}
